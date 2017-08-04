/*
Copyright 2016 The Rook Authors. All rights reserved.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package api for the operator api manager.
package api

import (
	"fmt"
	"net/http"
	"time"

	"github.com/coreos/pkg/capnslog"
	"github.com/rook/rook/pkg/clusterd"
	"github.com/rook/rook/pkg/model"
	"github.com/rook/rook/pkg/operator/k8sutil"
	opmon "github.com/rook/rook/pkg/operator/mon"
	rookclient "github.com/rook/rook/pkg/rook/client"
	"k8s.io/api/core/v1"
	extensions "k8s.io/api/extensions/v1beta1"
	"k8s.io/api/rbac/v1beta1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
)

var logger = capnslog.NewPackageLogger("github.com/rook/rook", "op-api")

const (
	deploymentName = "rook-api"
	clientTimeout  = 15 * time.Second
)

var clusterAccessRules = []v1beta1.PolicyRule{
	{
		APIGroups: []string{""},
		Resources: []string{"namespaces", "secrets", "pods", "services", "nodes", "configmaps", "events"},
		Verbs:     []string{"get", "list", "watch", "create", "update"},
	},
	{
		APIGroups: []string{"extensions"},
		Resources: []string{"thirdpartyresources", "deployments", "daemonsets", "replicasets"},
		Verbs:     []string{"get", "list", "create"},
	},
	{
		APIGroups: []string{"apiextensions.k8s.io"},
		Resources: []string{"customresourcedefinitions"},
		Verbs:     []string{"get", "list", "create"},
	},
	{
		APIGroups: []string{"storage.k8s.io"},
		Resources: []string{"storageclasses"},
		Verbs:     []string{"get", "list"},
	},
}

// Cluster has the api service properties
type Cluster struct {
	context   *clusterd.Context
	Namespace string
	placement k8sutil.Placement
	Version   string
	Replicas  int32
}

// New creates an instance
func New(context *clusterd.Context, namespace, version string, placement k8sutil.Placement) *Cluster {
	return &Cluster{
		context:   context,
		Namespace: namespace,
		placement: placement,
		Version:   version,
		Replicas:  1,
	}
}

// Start the api service
func (c *Cluster) Start() error {
	logger.Infof("starting the Rook api")

	// start the service
	err := c.startService()
	if err != nil {
		return fmt.Errorf("failed to start api service. %+v", err)
	}

	// create the artifacts for the api service to work with RBAC enabled
	err = c.makeClusterRole()
	if err != nil {
		logger.Warningf("failed to init RBAC for the api service. %+v", err)
	}

	// start the deployment
	deployment := c.makeDeployment()
	_, err = c.context.Clientset.ExtensionsV1beta1().Deployments(c.Namespace).Create(deployment)
	if err != nil {
		if !errors.IsAlreadyExists(err) {
			return fmt.Errorf("failed to create api deployment. %+v", err)
		}
		logger.Infof("api deployment already exists")
	} else {
		logger.Infof("api deployment started")
	}

	return nil
}

// make a cluster role
func (c *Cluster) makeClusterRole() error {
	account := &v1.ServiceAccount{}
	account.Name = deploymentName
	account.Namespace = c.Namespace
	_, err := c.context.Clientset.CoreV1().ServiceAccounts(c.Namespace).Create(account)
	if err != nil && !errors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create api service account. %+v", err)
	}

	// Create the cluster role if it doesn't yet exist.
	// If the role already exists we have to update it. Otherwise if the permissions change during an upgrade,
	// the create will fail with an error that we're changing the permissions.
	role := &v1beta1.ClusterRole{Rules: clusterAccessRules}
	role.Name = deploymentName
	_, err = c.context.Clientset.RbacV1beta1().ClusterRoles().Get(role.Name, metav1.GetOptions{})
	if errors.IsNotFound(err) {
		logger.Infof("creating cluster role rook-api")
		_, err = c.context.Clientset.RbacV1beta1().ClusterRoles().Create(role)
	} else if err == nil {
		logger.Infof("cluster role rook-api already exists. updating if needed.")
		_, err = c.context.Clientset.RbacV1beta1().ClusterRoles().Update(role)
	}
	if err != nil {
		return fmt.Errorf("failed to create cluster roles. %+v", err)
	}

	binding := &v1beta1.ClusterRoleBinding{}
	binding.Name = deploymentName
	binding.RoleRef = v1beta1.RoleRef{Name: deploymentName, Kind: "ClusterRole", APIGroup: "rbac.authorization.k8s.io"}
	binding.Subjects = []v1beta1.Subject{{Kind: "ServiceAccount", Name: deploymentName, Namespace: c.Namespace}}
	_, err = c.context.Clientset.RbacV1beta1().ClusterRoleBindings().Create(binding)
	if err != nil && !errors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create api cluster role binding. %+v", err)
	}
	return nil
}

func (c *Cluster) makeDeployment() *extensions.Deployment {
	deployment := &extensions.Deployment{}
	deployment.Name = deploymentName
	deployment.Namespace = c.Namespace

	podSpec := v1.PodSpec{
		ServiceAccountName: deploymentName,
		Containers:         []v1.Container{c.apiContainer()},
		RestartPolicy:      v1.RestartPolicyAlways,
		Volumes: []v1.Volume{
			{Name: k8sutil.DataDirVolume, VolumeSource: v1.VolumeSource{EmptyDir: &v1.EmptyDirVolumeSource{}}},
		},
	}
	c.placement.ApplyToPodSpec(&podSpec)

	podTemplateSpec := v1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{
			Name:        deploymentName,
			Labels:      c.getLabels(),
			Annotations: map[string]string{},
		},
		Spec: podSpec,
	}

	deployment.Spec = extensions.DeploymentSpec{Template: podTemplateSpec, Replicas: &c.Replicas}

	return deployment
}

func (c *Cluster) apiContainer() v1.Container {

	return v1.Container{
		Args: []string{
			"api",
			fmt.Sprintf("--config-dir=%s", k8sutil.DataDir),
			fmt.Sprintf("--port=%d", model.Port),
		},
		Name:  deploymentName,
		Image: k8sutil.MakeRookImage(c.Version),
		VolumeMounts: []v1.VolumeMount{
			{Name: k8sutil.DataDirVolume, MountPath: k8sutil.DataDir},
		},
		Env: []v1.EnvVar{
			{Name: "ROOK_VERSION_TAG", Value: c.Version},
			k8sutil.NamespaceEnvVar(),
			k8sutil.RepoPrefixEnvVar(),
			opmon.SecretEnvVar(),
			opmon.AdminSecretEnvVar(),
			opmon.EndpointEnvVar(),
			opmon.ClusterNameEnvVar(c.Namespace),
		},
	}
}

func (c *Cluster) startService() error {
	labels := c.getLabels()
	s := &v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      deploymentName,
			Namespace: c.Namespace,
			Labels:    labels,
		},
		Spec: v1.ServiceSpec{
			Ports: []v1.ServicePort{
				{
					Name:       deploymentName,
					Port:       model.Port,
					TargetPort: intstr.FromInt(int(model.Port)),
					Protocol:   v1.ProtocolTCP,
				},
			},
			Selector: labels,
		},
	}

	s, err := c.context.Clientset.CoreV1().Services(c.Namespace).Create(s)
	if err != nil {
		if !errors.IsAlreadyExists(err) {
			return fmt.Errorf("failed to create api service. %+v", err)
		}
		logger.Infof("api service already running")
		return nil
	}

	logger.Infof("API service running at %s:%d", s.Spec.ClusterIP, model.Port)
	return nil
}

func (c *Cluster) getLabels() map[string]string {
	return map[string]string{
		k8sutil.AppAttr:     deploymentName,
		k8sutil.ClusterAttr: c.Namespace,
	}
}

// GetRookClient gets a reference of the rook client connected to the Rook-API on the given namespace
func GetRookClient(namespace string, client kubernetes.Interface) (rookclient.RookRestClient, error) {

	// Look up the api service for the given namespace
	logger.Infof("retrieving rook api endpoint for namespace %s", namespace)
	svc, err := client.CoreV1().Services(namespace).Get(deploymentName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to find the api service. %+v", err)
	}

	httpClient := http.DefaultClient
	httpClient.Timeout = clientTimeout
	endpoint := fmt.Sprintf("%s:%d", svc.Spec.ClusterIP, svc.Spec.Ports[0].Port)
	rclient := rookclient.NewRookNetworkRestClient(rookclient.GetRestURL(endpoint), httpClient)
	logger.Infof("rook api endpoint %s for namespace %s", endpoint, namespace)
	return rclient, nil
}
