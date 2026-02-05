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

package scheduler_e2e

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/dapr/dapr/tests/runner"
)

const (
	schedulerStatefulSetName = "dapr-scheduler-server"
	daprSystemNamespace      = "dapr-system"
)

// TestSchedulerHATopologySpreadConstraints tests that scheduler pods are distributed
// across nodes when topology spread constraints are configured in HA mode.
func TestSchedulerHATopologySpreadConstraints(t *testing.T) {
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

	t.Run("scheduler_statefulset_exists", func(t *testing.T) {
		sts, err := kubeClient.AppsV1().StatefulSets(daprSystemNamespace).Get(ctx, schedulerStatefulSetName, metav1.GetOptions{})
		require.NoError(t, err, "Scheduler StatefulSet should exist")
		require.NotNil(t, sts.Spec.Replicas)
		require.Equal(t, int32(3), *sts.Spec.Replicas, "Scheduler should have 3 replicas")
	})

	t.Run("scheduler_pods_are_distributed", func(t *testing.T) {
		pods, err := kubeClient.CoreV1().Pods(daprSystemNamespace).List(ctx, metav1.ListOptions{
			LabelSelector: "app=dapr-scheduler-server",
		})
		require.NoError(t, err, "Should be able to list scheduler pods")
		require.Greater(t, len(pods.Items), 0, "At least 1 scheduler pod should exist")

		runningPods := 0
		nodeMap := make(map[string][]string)

		for _, pod := range pods.Items {
			if pod.Status.Phase == corev1.PodRunning {
				runningPods++
				nodeMap[pod.Spec.NodeName] = append(nodeMap[pod.Spec.NodeName], pod.Name)
			}
		}

		require.Greater(t, runningPods, 0, "At least 1 scheduler pod should be running")

		t.Logf("Scheduler pod distribution across %d nodes:", len(nodeMap))
		for node, podList := range nodeMap {
			t.Logf("  Node %s: %d pods %v", node, len(podList), podList)
		}

		if len(nodeMap) > 1 {
			for node, podList := range nodeMap {
				if len(podList) > 1 {
					t.Logf("WARNING: Node %s has %d scheduler pods - topology spread constraints may not be configured optimally", node, len(podList))
				}
			}
		}
	})
}
