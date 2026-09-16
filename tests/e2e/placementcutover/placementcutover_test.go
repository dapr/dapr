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
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
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
	actorlogsURLFormat   = "%s/test/logs"
	placementStatefulSet = "dapr-placement-server"
	schedulerMetricsPort = 9090
)

var tr *runner.TestRunner

func TestMain(m *testing.M) {
	// The cutover moves the cluster's placement authority, so this package is
	// excluded from the parallel pass of test-e2e-all: the scheduler
	// placement tail runs it serially, before and after the second pass,
	// with DAPR_E2E_PLACEMENT_CUTOVER set. The tests fatal without it.
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

// requireCutoverRun makes a run without the cutover variable loud rather
// than silently green: reaching a test on kubernetes without the variable
// means a makefile refactor lost the scheduler placement tail.
func requireCutoverRun(t *testing.T) {
	t.Helper()
	if _, ok := tr.Platform.(*runner.KubeTestPlatform); !ok {
		t.Skip("skipping test; only supported on kubernetes")
	}
	if os.Getenv("DAPR_E2E_PLACEMENT_CUTOVER") != "true" {
		t.Fatal("DAPR_E2E_PLACEMENT_CUTOVER is not set: the cutover moves the cluster's placement authority, run this package through the scheduler placement tail of test-e2e-all")
	}
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

type actorLogEntry struct {
	Action    string `json:"action,omitempty"`
	ActorType string `json:"actorType,omitempty"`
	ActorID   string `json:"actorId,omitempty"`
	Timestamp int    `json:"timestamp,omitempty"`
}

// assertSingleActivationWindow asserts the actor's activation log strictly
// alternates activation and deactivation and ends active: the ID was never
// activated twice without a deactivation between, which is the invariant
// the authority move must preserve.
func assertSingleActivationWindow(t *testing.T, externalURL, actorID string) {
	t.Helper()
	resp, err := utils.HTTPGet(fmt.Sprintf(actorlogsURLFormat, externalURL))
	require.NoError(t, err)
	var entries []actorLogEntry
	require.NoError(t, json.Unmarshal(resp, &entries))

	expect := "activation"
	last := ""
	for _, entry := range entries {
		if entry.ActorID != actorID ||
			(entry.Action != "activation" && entry.Action != "deactivation") {
			continue
		}
		require.Equalf(t, expect, entry.Action,
			"actor %q saw %s twice in a row: %+v", actorID, entry.Action, entries)
		if expect == "activation" {
			expect = "deactivation"
		} else {
			expect = "activation"
		}
		last = entry.Action
	}
	require.Equal(t, "activation", last,
		"actor %q must be active after its invocation", actorID)
}

// schedulerPlacementStreams sums dapr_scheduler_placement_streams_connected
// over the scheduler pods, which is the sidecars' adoption of the scheduler
// as the placement authority.
func schedulerPlacementStreams(client *kube.KubeClient) (float64, error) {
	pods, err := client.Pods(daprNamespace()).List(context.Background(), metav1.ListOptions{
		LabelSelector: "app=dapr-scheduler-server",
	})
	if err != nil {
		return 0, err
	}
	if len(pods.Items) == 0 {
		return 0, fmt.Errorf("no scheduler pods found in %q", daprNamespace())
	}

	var total float64
	for _, pod := range pods.Items {
		fw := kube.NewPodPortForwarder(client, daprNamespace())
		ports, ferr := fw.Connect(pod.Name, schedulerMetricsPort)
		if ferr != nil {
			fw.Close()
			continue
		}
		body, status, gerr := utils.HTTPGetWithStatus(fmt.Sprintf("http://localhost:%d/metrics", ports[0]))
		fw.Close()
		if gerr != nil || status != 200 {
			continue
		}
		for line := range strings.SplitSeq(string(body), "\n") {
			if !strings.HasPrefix(line, "dapr_scheduler_placement_streams_connected") {
				continue
			}
			fields := strings.Fields(line)
			if v, perr := strconv.ParseFloat(fields[len(fields)-1], 64); perr == nil {
				total += v
			}
		}
	}
	return total, nil
}

// appPods returns UID per app pod, keyed by pod name, requiring no container
// has restarted.
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
// their daprd sidecars, keep running: the same actor works before and after
// with no restarts, no double activation, and the sidecar actually adopts
// the scheduler.
func TestPlacementToScheduler(t *testing.T) {
	requireCutoverRun(t)
	externalURL := tr.Platform.AcquireAppExternalURL(appName)
	require.NotEmpty(t, externalURL)
	_, err := utils.HTTPGetNTimes(externalURL, numHealthChecks)
	require.NoError(t, err)

	// Actors work with the placement service as the authority, and no
	// sidecar holds a scheduler placement stream.
	waitPlacementStatefulSet(t, true)
	client := kubeClient(t)
	const actorID = "cutover-continuity"
	invokeActorEventually(t, externalURL, actorID)
	podsBefore := appPods(t)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		streams, serr := schedulerPlacementStreams(client)
		if assert.NoError(c, serr) {
			assert.Zero(c, streams)
		}
	}, time.Minute*3, time.Second*2)

	// The one helm value moves the authority to the scheduler.
	helmSetSchedulerPlacement(t, true)
	waitPlacementStatefulSet(t, false)

	// The same actor keeps working, and the sidecar's placement stream
	// moved to the scheduler: the authority moved, it was not left behind.
	invokeActorEventually(t, externalURL, actorID)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		streams, serr := schedulerPlacementStreams(client)
		if assert.NoError(c, serr) {
			assert.Positive(c, streams)
		}
	}, time.Minute*3, time.Second*2)
	assertSingleActivationWindow(t, externalURL, actorID)

	// The same pods, never restarted: the running sidecars adopted the new
	// authority live.
	require.Equal(t, podsBefore, appPods(t))
}

// TestSchedulerToPlacement asserts the rollback direction the same way: the
// helm value flips back, the placement service redeploys, and the running
// sidecars return to it without restarts.
func TestSchedulerToPlacement(t *testing.T) {
	requireCutoverRun(t)
	externalURL := tr.Platform.AcquireAppExternalURL(appName)
	require.NotEmpty(t, externalURL)
	_, err := utils.HTTPGetNTimes(externalURL, numHealthChecks)
	require.NoError(t, err)

	// Actors work with the scheduler as the authority.
	waitPlacementStatefulSet(t, false)
	client := kubeClient(t)
	const actorID = "rollback-continuity"
	invokeActorEventually(t, externalURL, actorID)
	podsBefore := appPods(t)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		streams, serr := schedulerPlacementStreams(client)
		if assert.NoError(c, serr) {
			assert.Positive(c, streams)
		}
	}, time.Minute*3, time.Second*2)

	helmSetSchedulerPlacement(t, false)
	waitPlacementStatefulSet(t, true)

	// The same actor keeps working, and the sidecar defected back: no
	// scheduler placement stream remains.
	invokeActorEventually(t, externalURL, actorID)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		streams, serr := schedulerPlacementStreams(client)
		if assert.NoError(c, serr) {
			assert.Zero(c, streams)
		}
	}, time.Minute*3, time.Second*2)
	assertSingleActivationWindow(t, externalURL, actorID)

	require.Equal(t, podsBefore, appPods(t))
}
