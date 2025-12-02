/*
Copyright 2025 The Dapr Authors
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

package workflow

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() {
	suite.Register(new(unhealthy))
}

type unhealthy struct {
	workflow  *workflow.Workflow
	logline   *logline.LogLine
	appHealth atomic.Bool

	sentUnhealthySignal atomic.Bool
}

func (u *unhealthy) Setup(t *testing.T) []framework.Option {
	u.appHealth.Store(true)

	handler := http.NewServeMux()
	handler.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if u.appHealth.Load() {
			w.WriteHeader(http.StatusOK)
			return
		}

		u.sentUnhealthySignal.Store(true)
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	app := prochttp.New(t,
		prochttp.WithHandler(handler),
	)

	u.logline = logline.New(t,
		logline.WithStdoutLineContains(
			`unhealthy-test::0::1: received cancellation signal while waiting for activity execution 'run-activity'`,
			`unhealthy-test::1::1: received cancellation signal while waiting for activity execution 'run-activity'`,
			`unhealthy-test::2::1: received cancellation signal while waiting for activity execution 'run-activity'`,
			`unhealthy-test::3::1: received cancellation signal while waiting for activity execution 'run-activity'`,
			`unhealthy-test::4::1: received cancellation signal while waiting for activity execution 'run-activity'`,
			`unhealthy-test::5::1: received cancellation signal while waiting for activity execution 'run-activity'`,
			`unhealthy-test::6::1: received cancellation signal while waiting for activity execution 'run-activity'`,
			`unhealthy-test::7::1: received cancellation signal while waiting for activity execution 'run-activity'`,
			`unhealthy-test::8::1: received cancellation signal while waiting for activity execution 'run-activity'`,
			`unhealthy-test::9::1: received cancellation signal while waiting for activity execution 'run-activity'`,
			`unhealthy-test::10::1: received cancellation signal while waiting for activity execution 'run-activity'`,

			`unknown instance ID/task ID combo: unhealthy-test/0"`,
			`unknown instance ID/task ID combo: unhealthy-test/1"`,
			`unknown instance ID/task ID combo: unhealthy-test/2"`,
			`unknown instance ID/task ID combo: unhealthy-test/3"`,
			`unknown instance ID/task ID combo: unhealthy-test/4"`,
			`unknown instance ID/task ID combo: unhealthy-test/5"`,
			`unknown instance ID/task ID combo: unhealthy-test/6"`,
			`unknown instance ID/task ID combo: unhealthy-test/7"`,
			`unknown instance ID/task ID combo: unhealthy-test/8"`,
			`unknown instance ID/task ID combo: unhealthy-test/9"`,
			`unknown instance ID/task ID combo: unhealthy-test/10"`,
		),
	)

	u.workflow = workflow.New(t,
		workflow.WithDaprdOptions(0,
			daprd.WithAppPort(app.Port()),
			daprd.WithAppHealthCheck(true),
			daprd.WithAppHealthCheckPath("/healthz"),
			daprd.WithAppHealthProbeInterval(1),
			daprd.WithAppHealthProbeThreshold(1),
			daprd.WithLogLineStdout(u.logline),
		),
	)
	return []framework.Option{
		framework.WithProcesses(u.logline, app, u.workflow),
	}
}

func (u *unhealthy) Run(t *testing.T, ctx context.Context) {
	u.workflow.WaitUntilRunning(t, ctx)

	const n = 10

	var inActivity atomic.Int64
	releaseCh := make(chan struct{})
	u.workflow.Registry().AddOrchestratorN("bar", func(ctx *task.OrchestrationContext) (any, error) {
		for range n {
			ctx.CallActivity("foo")
		}
		require.NoError(t, ctx.CallActivity("foo").Await(nil))
		return nil, nil
	})
	u.workflow.Registry().AddActivityN("foo", func(ctx task.ActivityContext) (any, error) {
		inActivity.Add(1)
		<-releaseCh
		return nil, nil
	})

	client := u.workflow.BackendClient(t, ctx)

	id, err := client.ScheduleNewOrchestration(ctx, "bar", api.WithInstanceID("unhealthy-test"))
	require.NoError(t, err)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(t, int64(n+1), inActivity.Load())
	}, time.Second*10, time.Millisecond*10)

	u.appHealth.Store(false)
	assert.Eventually(t, u.sentUnhealthySignal.Load, time.Second*10, time.Millisecond*10)

	close(releaseCh)

	_, err = client.WaitForOrchestrationCompletion(ctx, id)
	require.NoError(t, err)

	u.logline.EventuallyFoundAll(t)

	time.Sleep(time.Second * 4)

	for i := range n {
		assert.Equal(t, 1, strings.Count(
			string(u.logline.StdoutBuffer()),
			fmt.Sprintf(`unknown instance ID/task ID combo: unhealthy-test/%d"`, i),
		), "expected exactly one error log for activity task ID %d", i)
	}
}
