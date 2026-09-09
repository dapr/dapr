//go:build e2e

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

package placementcutover

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/dapr/dapr/tests/e2e/utils"
	kube "github.com/dapr/dapr/tests/platforms/kubernetes"
	"github.com/dapr/dapr/tests/runner"
)

const (
	appName              = "placementcutoverapp"
	numHealthChecks      = 60
	actorInvokeURLFormat = "%s/test/testactor/%s/method/actormethod"
	placementStatefulSet = "dapr-placement-server"
)

var tr *runner.TestRunner

func TestMain(m *testing.M) {
	// The cutover moves the cluster's placement authority, so this test can
	// not run inside the parallel suite. The scheduler placement tail of
	// test-e2e-all runs it serially, before and after the second pass.
	if os.Getenv("DAPR_E2E_PLACEMENT_CUTOVER") != "true" {
		fmt.Fprintln(os.Stdout, "skipping placement cutover, DAPR_E2E_PLACEMENT_CUTOVER is not set")
		os.Exit(0)
	}

	utils.SetupLogs("placementcutover")
	utils.InitHTTPClient(true)

	testApps := []kube.AppDescription{
		{
			AppName:             appName,
			DaprEnabled:         true,
			ImageName:           "e2e-actorapp",
			DebugLoggingEnabled: true,
			Replicas:            1,
			IngressEnabled:      true,
			MetricsEnabled:      true,
		},
	}

	tr = runner.NewTestRunner(appName, testApps, nil, nil)
	os.Exit(tr.Start(m))
}

func daprNamespace() string {
	if ns, ok := os.LookupEnv("DAPR_TEST_NAMESPACE"); ok && ns != "" {
		return ns
	}
	return "dapr-tests"
}

func kubeClient(t *testing.T) *kube.KubeClient {
	t.Helper()
	platform, ok := tr.Platform.(*runner.KubeTestPlatform)
	if !ok {
		t.Skip("skipping test; only supported on kubernetes")
	}
	return platform.KubeClient
}

// helmSetSchedulerPlacement flips the one helm value which moves the
// placement authority, reusing every other value of the deployed release.
func helmSetSchedulerPlacement(t *testing.T, enabled bool) {
	t.Helper()
	require.NoError(t, utils.HelmUpgradeDapr(daprNamespace(),
		fmt.Sprintf("global.scheduler.placement.enabled=%t", enabled)))
}

// waitPlacementStatefulSet waits for the placement StatefulSet to be
// undeployed, or deployed with ready replicas, matching the helm value.
func waitPlacementStatefulSet(t *testing.T, present bool) {
	t.Helper()
	client := kubeClient(t)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		sts, err := client.ClientSet.AppsV1().StatefulSets(daprNamespace()).
			Get(context.Background(), placementStatefulSet, metav1.GetOptions{})
		if !present {
			assert.Truef(c, apierrors.IsNotFound(err),
				"the placement statefulset must be undeployed, got err=%v", err)
			return
		}
		if !assert.NoError(c, err) {
			return
		}
		assert.Positive(c, sts.Status.ReadyReplicas)
	}, time.Minute*5, time.Second*2)
}

func invokeActorEventually(t *testing.T, externalURL, actorID string) {
	t.Helper()
	url := fmt.Sprintf(actorInvokeURLFormat, externalURL, actorID)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		_, status, err := utils.HTTPPostWithStatus(url, []byte{})
		if !assert.NoError(c, err) {
			return
		}
		assert.Equal(c, 200, status)
	}, time.Minute*3, time.Second*2)
}

// appPods returns UID and restart count per app pod, keyed by pod name.
func appPods(t *testing.T) map[string]types.UID {
	t.Helper()
	client := kubeClient(t)
	pods, err := client.Pods(daprNamespace()).List(context.Background(), metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=%s", kube.TestAppLabelKey, appName),
	})
	require.NoError(t, err)
	require.NotEmpty(t, pods.Items)
	uids := make(map[string]types.UID, len(pods.Items))
	for _, pod := range pods.Items {
		uids[pod.Name] = pod.UID
		for _, cs := range pod.Status.ContainerStatuses {
			require.Zerof(t, cs.RestartCount,
				"container %s of pod %s restarted", cs.Name, pod.Name)
		}
	}
	return uids
}

// TestPlacementToScheduler asserts the helm toggle moves actor placement
// from the placement service to the scheduler while the app's pods, and
// their daprd sidecars, keep running: actors work before and after with no
// restarts and no redeployments.
func TestPlacementToScheduler(t *testing.T) {
	externalURL := tr.Platform.AcquireAppExternalURL(appName)
	require.NotEmpty(t, externalURL)
	_, err := utils.HTTPGetNTimes(externalURL, numHealthChecks)
	require.NoError(t, err)

	// Actors work with the placement service as the authority.
	waitPlacementStatefulSet(t, true)
	invokeActorEventually(t, externalURL, "cutover-before")
	podsBefore := appPods(t)

	// The one helm value moves the authority to the scheduler.
	helmSetSchedulerPlacement(t, true)
	waitPlacementStatefulSet(t, false)

	// Actors keep working, served by scheduler placement.
	invokeActorEventually(t, externalURL, "cutover-after")

	// The same pods, never restarted: the running sidecars adopted the new
	// authority live.
	require.Equal(t, podsBefore, appPods(t))
}

// TestSchedulerToPlacement asserts the rollback direction the same way: the
// helm value flips back, the placement service redeploys, and the running
// sidecars return to it without restarts.
func TestSchedulerToPlacement(t *testing.T) {
	externalURL := tr.Platform.AcquireAppExternalURL(appName)
	require.NotEmpty(t, externalURL)
	_, err := utils.HTTPGetNTimes(externalURL, numHealthChecks)
	require.NoError(t, err)

	// Actors work with the scheduler as the authority.
	waitPlacementStatefulSet(t, false)
	invokeActorEventually(t, externalURL, "rollback-before")
	podsBefore := appPods(t)

	helmSetSchedulerPlacement(t, false)
	waitPlacementStatefulSet(t, true)

	invokeActorEventually(t, externalURL, "rollback-after")
	require.Equal(t, podsBefore, appPods(t))
}
