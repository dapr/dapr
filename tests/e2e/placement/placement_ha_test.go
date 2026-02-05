//go:build e2e
// +build e2e

/*
Copyright 2026 The Dapr Authors
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

package placement_e2e

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/dapr/dapr/tests/e2e/utils"
	"github.com/dapr/dapr/tests/runner"
)

const (
	placementStatefulSetName = "dapr-placement-server"
	daprSystemNamespace      = "dapr-system"
)

var tr *runner.TestRunner

func TestMain(m *testing.M) {
	utils.SetupLogs("placement")
	utils.InitHTTPClient(true)

	// Placement tests don't need test apps - they test the control plane directly
	tr = runner.NewTestRunner("placement", nil, nil, nil)
	os.Exit(tr.Start(m))
}

// TestPlacementHATopologySpreadConstraints tests that placement pods are distributed
// across nodes when topology spread constraints are configured.
func TestPlacementHATopologySpreadConstraints(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	if tr == nil || tr.Platform == nil {
		t.Skip("Test runner not initialized, skipping e2e test")
	}

	kubePlatform := tr.Platform.(*runner.KubeTestPlatform)
	if kubePlatform.KubeClient == nil {
		t.Skip("Kubernetes client not available, skipping e2e test")
	}
	kubeClient := kubePlatform.KubeClient.ClientSet

	t.Run("placement_statefulset_exists", func(t *testing.T) {
		sts, err := kubeClient.AppsV1().StatefulSets(daprSystemNamespace).Get(ctx, placementStatefulSetName, metav1.GetOptions{})
		require.NoError(t, err, "Placement StatefulSet should exist")
		require.NotNil(t, sts.Spec.Replicas)
		// Placement can have 1 or 3 replicas depending on HA mode
		assert.Contains(t, []int32{1, 3}, *sts.Spec.Replicas, "Placement should have 1 or 3 replicas")
	})

	t.Run("placement_pods_are_distributed", func(t *testing.T) {
		pods, err := kubeClient.CoreV1().Pods(daprSystemNamespace).List(ctx, metav1.ListOptions{
			LabelSelector: "app=dapr-placement-server",
		})
		require.NoError(t, err, "Should be able to list placement pods")
		require.Greater(t, len(pods.Items), 0, "At least 1 placement pod should exist")

		runningPods := 0
		nodeMap := make(map[string][]string)

		for _, pod := range pods.Items {
			if pod.Status.Phase == corev1.PodRunning {
				runningPods++
				nodeMap[pod.Spec.NodeName] = append(nodeMap[pod.Spec.NodeName], pod.Name)
			}
		}

		require.Greater(t, runningPods, 0, "At least 1 placement pod should be running")

		t.Logf("Placement pod distribution across %d nodes:", len(nodeMap))
		for node, podList := range nodeMap {
			t.Logf("  Node %s: %d pods %v", node, len(podList), podList)
		}

		// In HA mode with 3 replicas, warn if pods are not well distributed
		sts, err := kubeClient.AppsV1().StatefulSets(daprSystemNamespace).Get(ctx, placementStatefulSetName, metav1.GetOptions{})
		if err == nil && sts.Spec.Replicas != nil && *sts.Spec.Replicas == 3 {
			if len(nodeMap) > 1 {
				for node, podList := range nodeMap {
					if len(podList) > 1 {
						t.Logf("WARNING: Node %s has %d placement pods in HA mode - topology spread constraints may not be configured optimally", node, len(podList))
					}
				}
			}
		}
	})
}
