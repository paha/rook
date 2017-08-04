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

// Package mds for file systems.
package mds

import (
	"fmt"

	"github.com/coreos/pkg/capnslog"
	cephmds "github.com/rook/rook/pkg/ceph/mds"
	"github.com/rook/rook/pkg/clusterd"
	"github.com/rook/rook/pkg/operator/k8sutil"
	opmon "github.com/rook/rook/pkg/operator/mon"
	"k8s.io/api/core/v1"
	extensions "k8s.io/api/extensions/v1beta1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

var logger = capnslog.NewPackageLogger("github.com/rook/rook", "op-mds")

const (
	appName            = "rook-ceph-mds"
	dataPoolSuffix     = "-data"
	metadataPoolSuffix = "-metadata"
	keyringName        = "keyring"
)

// Cluster for mds management
type Cluster struct {
	Namespace string
	Version   string
	Replicas  int32
	context   *clusterd.Context
	dataDir   string
	placement k8sutil.Placement
}

// New creates an instance of the mds manager
func New(context *clusterd.Context, namespace, version string, placement k8sutil.Placement) *Cluster {
	return &Cluster{
		context:   context,
		Namespace: namespace,
		placement: placement,
		Version:   version,
		Replicas:  1,
		dataDir:   k8sutil.DataDir,
	}
}

// Start the mds manager
func (c *Cluster) Start() error {
	logger.Infof("start running mds")

	id := "mds1"
	err := c.createKeyring(c.context.Clientset, id)
	if err != nil {
		return fmt.Errorf("failed to create mds keyring. %+v", err)
	}

	// start the deployment
	deployment := c.makeDeployment(id)
	_, err = c.context.Clientset.ExtensionsV1beta1().Deployments(c.Namespace).Create(deployment)
	if err != nil {
		if !errors.IsAlreadyExists(err) {
			return fmt.Errorf("failed to create mds deployment. %+v", err)
		}
		logger.Infof("mds deployment already exists")
	} else {
		logger.Infof("mds deployment started")
	}

	return nil
}

func (c *Cluster) createKeyring(clientset kubernetes.Interface, id string) error {
	_, err := clientset.CoreV1().Secrets(c.Namespace).Get(appName, metav1.GetOptions{})
	if err == nil {
		logger.Infof("the mds keyring was already generated")
		return nil
	}
	if !errors.IsNotFound(err) {
		return fmt.Errorf("failed to get mds secrets. %+v", err)
	}

	// get-or-create-key for the user account
	keyring, err := cephmds.CreateKeyring(c.context, c.Namespace, id)
	if err != nil {
		return fmt.Errorf("failed to create mds keyring. %+v", err)
	}

	// Store the keyring in a secret
	secrets := map[string]string{
		keyringName: keyring,
	}
	secret := &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: appName, Namespace: c.Namespace},
		StringData: secrets,
		Type:       k8sutil.RookType,
	}
	_, err = clientset.CoreV1().Secrets(c.Namespace).Create(secret)
	if err != nil {
		return fmt.Errorf("failed to save mds secrets. %+v", err)
	}

	return nil
}

func (c *Cluster) makeDeployment(id string) *extensions.Deployment {
	deployment := &extensions.Deployment{}
	deployment.Name = appName
	deployment.Namespace = c.Namespace

	podSpec := v1.PodSpec{
		Containers:    []v1.Container{c.mdsContainer(id)},
		RestartPolicy: v1.RestartPolicyAlways,
		Volumes: []v1.Volume{
			{Name: k8sutil.DataDirVolume, VolumeSource: v1.VolumeSource{EmptyDir: &v1.EmptyDirVolumeSource{}}},
			k8sutil.ConfigOverrideVolume(),
		},
	}
	c.placement.ApplyToPodSpec(&podSpec)

	podTemplateSpec := v1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{
			Name:        appName,
			Labels:      c.getLabels(),
			Annotations: map[string]string{},
		},
		Spec: podSpec,
	}

	deployment.Spec = extensions.DeploymentSpec{Template: podTemplateSpec, Replicas: &c.Replicas}

	return deployment
}

func (c *Cluster) mdsContainer(id string) v1.Container {

	return v1.Container{
		Args: []string{
			"mds",
			fmt.Sprintf("--config-dir=%s", k8sutil.DataDir),
			fmt.Sprintf("--mds-id=%s", id),
		},
		Name:  appName,
		Image: k8sutil.MakeRookImage(c.Version),
		VolumeMounts: []v1.VolumeMount{
			{Name: k8sutil.DataDirVolume, MountPath: k8sutil.DataDir},
			k8sutil.ConfigOverrideMount(),
		},
		Env: []v1.EnvVar{
			{Name: "ROOK_MDS_KEYRING", ValueFrom: &v1.EnvVarSource{SecretKeyRef: &v1.SecretKeySelector{LocalObjectReference: v1.LocalObjectReference{Name: appName}, Key: keyringName}}},
			opmon.ClusterNameEnvVar(c.Namespace),
			opmon.EndpointEnvVar(),
			opmon.SecretEnvVar(),
			k8sutil.PodIPEnvVar(k8sutil.PrivateIPEnvVar),
			k8sutil.PodIPEnvVar(k8sutil.PublicIPEnvVar),
			opmon.AdminSecretEnvVar(),
			k8sutil.ConfigOverrideEnvVar(),
		},
	}
}

func (c *Cluster) getLabels() map[string]string {
	return map[string]string{
		k8sutil.AppAttr:     appName,
		k8sutil.ClusterAttr: c.Namespace,
	}
}
