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

package integration

import (
	"time"

	"github.com/coreos/pkg/capnslog"
	"github.com/rook/rook/pkg/model"
	"github.com/rook/rook/tests/framework/clients"
	"github.com/rook/rook/tests/framework/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

var (
	logger = capnslog.NewPackageLogger("github.com/rook/rook", "integrationTest")

	defaultNamespace = "default"
)

//Test to make sure all rook components are installed and Running
func checkIfRookClusterIsInstalled(s suite.Suite, k8sh *utils.K8sHelper, opNamespace string, clusterNamespace string) {
	logger.Infof("Make sure all Pods in Rook Cluster %s are running", clusterNamespace)
	assert.True(s.T(), k8sh.CheckPodCountAndState("rook-operator", opNamespace, 1, "Running"),
		"Make sure there is 1 rook-operator present in Running state")
	assert.True(s.T(), k8sh.CheckPodCountAndState("rook-agent", opNamespace, 1, "Running"),
		"Make sure there is 1 rook-agent present in Running state")
	assert.True(s.T(), k8sh.CheckPodCountAndState("rook-api", clusterNamespace, 1, "Running"),
		"Make sure there is 1 rook-api present in Running state")
	assert.True(s.T(), k8sh.CheckPodCountAndState("rook-ceph-mgr", clusterNamespace, 1, "Running"),
		"Make sure there is 1 rook-ceph-mgr present in Running state")
	assert.True(s.T(), k8sh.CheckPodCountAndState("rook-ceph-osd", clusterNamespace, 1, "Running"),
		"Make sure there is at lest 1 rook-ceph-osd present in Running state")
	assert.True(s.T(), k8sh.CheckPodCountAndState("rook-ceph-mon", clusterNamespace, 3, "Running"),
		"Make sure there are 3 rook-ceph-mon present in Running state")
}

func checkIfRookClusterIsHealthy(s suite.Suite, testClient *clients.TestClient, clusterNamespace string) {
	logger.Infof("Testing cluster %s health", clusterNamespace)
	var err error
	var status model.StatusDetails

	retryCount := 0
	for retryCount < utils.RetryLoop {
		status, err = clients.IsClusterHealthy(testClient)
		if err == nil {
			logger.Infof("cluster %s is healthy. final status: %+v", clusterNamespace, status)
			return
		}

		retryCount++
		logger.Infof("waiting for cluster %s to become healthy. err: %+v", clusterNamespace, err)
		<-time.After(time.Duration(utils.RetryInterval) * time.Second)
	}

	require.Nil(s.T(), err)
}

func gatherAllRookLogs(k8sh *utils.K8sHelper, s suite.Suite, hostType string, opNamespace string, clusterNamespace string) {
	logger.Infof("Gathering all logs from Rook Cluster %s", clusterNamespace)
	k8sh.GetRookLogs("rook-operator", hostType, opNamespace, s.T().Name())
	k8sh.GetRookLogs("rook-agent", hostType, opNamespace, s.T().Name())
	k8sh.GetRookLogs("rook-api", hostType, clusterNamespace, s.T().Name())
	k8sh.GetRookLogs("rook-ceph-mgr", hostType, clusterNamespace, s.T().Name())
	k8sh.GetRookLogs("rook-ceph-mon", hostType, clusterNamespace, s.T().Name())
	k8sh.GetRookLogs("rook-ceph-osd", hostType, clusterNamespace, s.T().Name())
	k8sh.GetRookLogs("rook-ceph-rgw", hostType, clusterNamespace, s.T().Name())
	k8sh.GetRookLogs("rook-ceph-mds", hostType, clusterNamespace, s.T().Name())
}
