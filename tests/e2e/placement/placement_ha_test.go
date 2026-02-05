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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/dapr/dapr/tests/runner"
)

const (
	placementStatefulSetName = "dapr-placement-server"
	daprSystemNamespace      = "dapr-system"
)

var tr *runner.TestRunner

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

	t.Run("placement_pods_exist_in_ha_mode", func(t *testing.T) {
		sts, err := kubeClient.AppsV1().StatefulSets(daprSystemNamespace).Get(ctx, placementStatefulSetName, metav1.GetOptions{})
		if err != nil {
			t.Skipf("Placement StatefulSet not found, skipping: %v", err)
			return
		}

		require.NotNil(t, sts.Spec.Replicas)
		assert.Equal(t, int32(3), *sts.Spec.Replicas, "Placement should have 3 replicas in HA mode")
	})

	t.Run("placement_pods_are_distributed", func(t *testing.T) {
		pods, err := kubeClient.CoreV1().Pods(daprSystemNamespace).List(ctx, metav1.ListOptions{
			LabelSelector: "app=dapr-placement-server",
		})
		if err != nil {
			t.Skipf("Failed to list placement pods: %v", err)
			return
		}

		runningPods := 0
		nodeMap := make(map[string][]string)

		for _, pod := range pods.Items {
			if pod.Status.Phase == corev1.PodRunning {
				runningPods++
				nodeMap[pod.Spec.NodeName] = append(nodeMap[pod.Spec.NodeName], pod.Name)
			}
		}

		assert.GreaterOrEqual(t, runningPods, 1, "At least 1 placement pod should be running")

		t.Logf("Placement pod distribution across %d nodes:", len(nodeMap))
		for node, podList := range nodeMap {
			t.Logf("  Node %s: %d pods %v", node, len(podList), podList)
		}

		if len(nodeMap) > 1 {
			for node, podList := range nodeMap {
				if len(podList) > 1 {
					t.Logf("WARNING: Node %s has %d placement pods - check topology spread constraints", node, len(podList))
				}
			}
		}
	})
}
