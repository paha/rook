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
package rgw

import (
	"fmt"
	"io/ioutil"
	"os"
	"testing"

	cephrgw "github.com/rook/rook/pkg/ceph/rgw"
	"github.com/rook/rook/pkg/clusterd"
	"github.com/rook/rook/pkg/operator/k8sutil"
	testop "github.com/rook/rook/pkg/operator/test"
	exectest "github.com/rook/rook/pkg/util/exec/test"
	"github.com/stretchr/testify/assert"
	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestStartRGW(t *testing.T) {
	clientset := testop.New(3)
	executor := &exectest.MockExecutor{
		MockExecuteCommandWithOutputFile: func(actionName string, command string, outFileArg string, args ...string) (string, error) {
			return "{\"key\":\"mysecurekey\"}", nil
		},
	}

	configDir, _ := ioutil.TempDir("", "")
	defer os.RemoveAll(configDir)
	c := New(&clusterd.Context{Clientset: clientset, Executor: executor, ConfigDir: configDir}, "ns", "version", k8sutil.Placement{})

	// start a basic cluster
	err := c.Start()
	assert.Nil(t, err)

	validateStart(t, c, clientset)

	// starting again should be a no-op
	err = c.Start()
	assert.Nil(t, err)

	validateStart(t, c, clientset)
}

func validateStart(t *testing.T, c *Cluster, clientset *fake.Clientset) {

	r, err := clientset.ExtensionsV1beta1().Deployments(c.Namespace).Get(appName, metav1.GetOptions{})
	assert.Nil(t, err)
	assert.Equal(t, appName, r.Name)

	s, err := clientset.CoreV1().Services(c.Namespace).Get(appName, metav1.GetOptions{})
	assert.Nil(t, err)
	assert.Equal(t, appName, s.Name)

	secret, err := clientset.CoreV1().Secrets(c.Namespace).Get(appName, metav1.GetOptions{})
	assert.Nil(t, err)
	assert.Equal(t, appName, secret.Name)
	assert.Equal(t, 1, len(secret.StringData))

}

func TestPodSpecs(t *testing.T) {
	c := New(nil, "ns", "myversion", k8sutil.Placement{})

	d := c.makeDeployment()
	assert.NotNil(t, d)
	assert.Equal(t, appName, d.Name)
	assert.Equal(t, v1.RestartPolicyAlways, d.Spec.Template.Spec.RestartPolicy)
	assert.Equal(t, 2, len(d.Spec.Template.Spec.Volumes))
	assert.Equal(t, "rook-data", d.Spec.Template.Spec.Volumes[0].Name)
	assert.Equal(t, k8sutil.ConfigOverrideName, d.Spec.Template.Spec.Volumes[1].Name)

	assert.Equal(t, appName, d.ObjectMeta.Name)
	assert.Equal(t, appName, d.Spec.Template.ObjectMeta.Labels["app"])
	assert.Equal(t, c.Namespace, d.Spec.Template.ObjectMeta.Labels["rook_cluster"])
	assert.Equal(t, 0, len(d.ObjectMeta.Annotations))

	cont := d.Spec.Template.Spec.Containers[0]
	assert.Equal(t, "rook/rook:myversion", cont.Image)
	assert.Equal(t, 2, len(cont.VolumeMounts))

	assert.Equal(t, "rgw", cont.Args[0])
	assert.Equal(t, "--config-dir=/var/lib/rook", cont.Args[1])
	assert.Equal(t, fmt.Sprintf("--rgw-port=%d", cephrgw.RGWPort), cont.Args[2])
	assert.Equal(t, fmt.Sprintf("--rgw-host=%s", cephrgw.DNSName), cont.Args[3])
}
