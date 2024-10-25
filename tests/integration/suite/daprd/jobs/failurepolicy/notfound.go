/* Copyright 2024 The Dapr Authors
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

package failurepolicy

import (
	"context"
	"net/http"
	"path"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/kit/concurrency/slice"
	"github.com/dapr/kit/ptr"
)

func init() {
	suite.Register(new(notfound))
}

// status NotFound does not trigger failure policy.
type notfound struct {
	scheduler *scheduler.Scheduler
	daprd     *daprd.Daprd
	triggered slice.Slice[string]
}

func (n *notfound) Setup(t *testing.T) []framework.Option {
	n.triggered = slice.String()

	app := app.New(t,
		app.WithHandlerFunc("/job/test", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			n.triggered.Append(path.Base(r.URL.Path))
		}),
	)

	n.scheduler = scheduler.New(t)

	n.daprd = daprd.New(t,
		daprd.WithAppPort(app.Port()),
		daprd.WithScheduler(n.scheduler),
	)

	return []framework.Option{
		framework.WithProcesses(app, n.scheduler, n.daprd),
	}
}

func (n *notfound) Run(t *testing.T, ctx context.Context) {
	n.scheduler.WaitUntilRunning(t, ctx)
	n.daprd.WaitUntilRunning(t, ctx)

	_, err := n.daprd.GRPCClient(t, ctx).ScheduleJobAlpha1(ctx, &rtv1.ScheduleJobRequest{
		Job: &rtv1.Job{
			Name:    "test",
			DueTime: ptr.Of("0s"),
		},
	})
	require.NoError(t, err)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.ElementsMatch(c, []string{"test"}, n.triggered.Slice())
	}, time.Second*10, time.Millisecond*10)

	time.Sleep(time.Second * 2)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.ElementsMatch(c, []string{"test"}, n.triggered.Slice())
	}, time.Second*10, time.Millisecond*10)
}
