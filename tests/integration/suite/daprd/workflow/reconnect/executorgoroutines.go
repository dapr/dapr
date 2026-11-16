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

package reconnect

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(executorgoroutines))
}

// executorgoroutines connects and disconnects the only workflow worker over
// and over. Every connect registers the workflow actor types, building new
// actor factories, and every disconnect unregisters them. No cycle may leave
// a goroutine behind.
type executorgoroutines struct {
	workflow *workflow.Workflow
}

func (e *executorgoroutines) Setup(t *testing.T) []framework.Option {
	e.workflow = workflow.New(t, workflow.WithClusteredDeployment(true))

	return []framework.Option{
		framework.WithProcesses(e.workflow),
	}
}

func (e *executorgoroutines) Run(t *testing.T, ctx context.Context) {
	e.workflow.WaitUntilRunning(t, ctx)

	d := e.workflow.Dapr()
	executorType := fmt.Sprintf("dapr.internal.%s.%s.executor", d.Namespace(), d.AppID())
	cycle := func() {
		worker := e.workflow.ConnectWorker(t, ctx, e.workflow.Registry())
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			_, hosted := d.ActiveActorCount(c, ctx, executorType)
			assert.True(c, hosted, "worker connect did not register the executor actor type")
		}, time.Second*10, time.Millisecond*10)

		worker.Disconnect(t)
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			_, hosted := d.ActiveActorCount(c, ctx, executorType)
			assert.False(c, hosted, "worker disconnect did not unregister the executor actor type")
		}, time.Second*10, time.Millisecond*10)
	}

	for range 3 {
		cycle()
	}
	baseline := e.workflow.Metrics(t, ctx)["go_goroutines"]

	const cycles = 40
	for range cycles {
		cycle()
	}

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.LessOrEqual(c, d.Metrics(c, ctx).All()["go_goroutines"], baseline+10,
			"%d worker reconnects left goroutines behind", cycles)
	}, time.Second*30, time.Millisecond*100)
}
