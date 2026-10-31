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

package niceness

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/framework/watchjobs"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(adaptive))
}

// adaptive verifies, with real workflows and no configured limit, that the
// scheduler notices an app's activities saturating something they depend on,
// derives a limit for them, and from then on keeps a light, latency
// sensitive activity (hi) fast while a flood of batch activities (lo) waits.
type adaptive struct {
	workflow *workflow.Workflow
}

const adaptiveAppID = "niceness-adaptive"

func (a *adaptive) Setup(t *testing.T) []framework.Option {
	configManifest := `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: nicenessadaptive
spec:
  workflow:
    niceness:
      default: 10
      workflows:
      - name: hiwf
        niceness: -10
      activities:
      - name: hi
        niceness: -10
`
	a.workflow = workflow.New(t,
		workflow.WithDaprdOptions(0, daprd.WithConfigManifests(t, configManifest), daprd.WithAppID(adaptiveAppID)),
	)

	return []framework.Option{
		framework.WithProcesses(a.workflow),
	}
}

func (a *adaptive) Run(t *testing.T, ctx context.Context) {
	a.workflow.WaitUntilRunning(t, ctx)

	// 2 activities at a time, 40ms each: 50 a second.
	dep := newBottleneck(2, 40*time.Millisecond)
	reg := a.workflow.Registry()
	reg.AddActivityN("hi", dep.activity(nil))
	reg.AddActivityN("lo", dep.activity(nil))
	reg.AddWorkflowN("hiwf", callActivity("hi"))
	reg.AddWorkflowN("lowf", callActivity("lo"))

	sched := a.workflow.Scheduler()
	cl := a.workflow.BackendClient(t, ctx)
	activityType := "dapr.internal.default." + adaptiveAppID + ".activity"

	loadCtx, stopLoad := context.WithCancel(ctx)
	defer stopLoad()

	// Light load, within capacity, sets each name's baseline latency. hi
	// keeps this rate throughout.
	generate(loadCtx, cl, "hiwf", 15)
	generate(loadCtx, cl, "lowf", 15)
	time.Sleep(4 * time.Second)
	assert.Zero(t, watchjobs.Metric(t, ctx, sched, "dapr_scheduler_concurrency_limit", activityType), "no limit while the app keeps up")

	// A flood of batch work, well past what the dependency can serve.
	generate(loadCtx, cl, "lowf", 120)

	ratio := waitForLimit(t, ctx, sched, activityType)
	assert.GreaterOrEqual(t, ratio, 2.0, "the activities' latency ratio crossed the overload threshold")
	assert.Zero(t, watchjobs.Metric(t, ctx, sched, "dapr_scheduler_concurrency_limit", "dapr.internal.default"), "a single app does not limit its namespace")

	// Let activities dispatched before the limit drain, then measure.
	time.Sleep(4 * time.Second)
	hiBefore, loBefore := waitsOf(t, ctx, sched, activityType+":hi"), waitsOf(t, ctx, sched, activityType+":lo")
	time.Sleep(4 * time.Second)
	hiAfter, loAfter := waitsOf(t, ctx, sched, activityType+":hi"), waitsOf(t, ctx, sched, activityType+":lo")
	stopLoad()

	require.Greater(t, hiAfter.count, hiBefore.count)
	require.Greater(t, loAfter.count, loBefore.count, "batch work still runs in the capacity hi leaves")
	hiWait, loWait := meanWait(hiBefore, hiAfter), meanWait(loBefore, loAfter)
	t.Logf("activity mean wait hi=%.1fms lo=%.1fms limit=%v", hiWait, loWait,
		watchjobs.Metric(t, ctx, sched, "dapr_scheduler_concurrency_limit", activityType))
	assert.Less(t, hiWait*5, loWait, "hi (niceness -10) waits a fraction of what lo (niceness 10) waits")
}
