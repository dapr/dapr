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

package workflow

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework/iowriter/logger"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

// Join starts d, a daprd of this harness's app that is not one of the
// framework's processes, connects a worker for registry to it and waits until
// placement disseminated a new table, so actors can move to d.
func (w *Workflow) Join(t *testing.T, ctx context.Context, d *daprd.Daprd, registry *task.TaskRegistry) {
	t.Helper()
	version := w.PlacementVersion(t, ctx)
	d.Run(t, ctx)
	t.Cleanup(func() { d.Cleanup(t) })
	d.WaitUntilRunning(t, ctx)

	cl := client.NewTaskHubGrpcClient(d.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, cl.StartWorkItemListener(ctx, registry))

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Greater(c, w.PlacementVersion(t, ctx), version,
			"dissemination must advance for each new daprd")
	}, time.Second*15, time.Millisecond*10)
}
