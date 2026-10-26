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

package fallback

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(settle))
}

// settle checks the settle period as a guard: a daprd without a worker,
// whose placement tables show no daprd of its app with an executor, stays a
// non-host when a worker connects to another daprd of the app within its
// settle period. Daprd 0 has a settle period of a millisecond (a zero one
// is taken as unset), so it turns into a fallback
// host on the first table without an executor, the table daprd 1 starts its
// settle period on. The worker connects to daprd 0 well within daprd 1's
// settle period, and daprd 1 hosts nothing past its end. Once the worker
// leaves, daprd 1 turns into a fallback host after its settle period.
type settle struct {
	workflow *workflow.Workflow
}

// settlePeriod is long enough for a worker to connect, and a placement round
// to show its executor, well within it.
const settlePeriod = 3 * time.Second

func (s *settle) Setup(t *testing.T) []framework.Option {
	appID := uuid.NewString()
	s.workflow = workflow.New(t,
		workflow.WithDaprds(2),
		workflow.WithStagedOperations(),
		workflow.WithDaprdOptions(0, daprd.WithAppID(appID), daprd.WithWorkflowFallbackSettle(t, time.Millisecond)),
		workflow.WithDaprdOptions(1, daprd.WithAppID(appID), daprd.WithWorkflowFallbackSettle(t, settlePeriod)),
	)

	return []framework.Option{
		framework.WithProcesses(s.workflow),
	}
}

func (s *settle) Run(t *testing.T, ctx context.Context) {
	s.workflow.WaitUntilRunning(t, ctx)

	hosts := func(c assert.TestingT, index int) bool {
		return s.workflow.HostsActorType(c, ctx, index, s.workflow.WorkflowActorType(index))
	}

	// Daprd 0 is a fallback host: the placement tables have shown no
	// executor, and daprd 1 waits out its settle period.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.True(c, hosts(c, 0))
	}, time.Second*10, time.Millisecond*10)
	tableWithoutExecutor := time.Now()
	require.False(t, hosts(t, 1), "daprd 1 hosts the workflow actor types before its settle period")

	before := s.workflow.PlacementVersion(t, ctx)
	worker := s.workflow.ConnectWorkerN(t, ctx, 0, s.workflow.RegistryN(0))
	s.workflow.WaitForConnectedWorkersN(t, ctx, 0, 1)
	s.workflow.WaitForActorTypePlaced(t, ctx, 0, s.workflow.ExecutorActorType(0), true, before)
	require.Less(t, time.Since(tableWithoutExecutor), settlePeriod-time.Second,
		"the worker connected too late in daprd 1's settle period for the test to tell")

	// Past the end of the settle period daprd 1 started on that table.
	assert.Never(t, func() bool {
		return hosts(t, 1)
	}, time.Until(tableWithoutExecutor.Add(settlePeriod+time.Second)), time.Millisecond*10,
		"daprd 1 hosted the workflow actor types although a worker connected within its settle period")

	// With no worker left, daprd 1 does turn into a fallback host, once its
	// settle period has passed.
	left := time.Now()
	worker.Disconnect(t)
	s.workflow.WaitForNoConnectedWorkersN(t, ctx, 0)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.True(c, hosts(c, 1))
	}, settlePeriod+time.Second*10, time.Millisecond*10)
	assert.GreaterOrEqual(t, time.Since(left), settlePeriod-time.Second)
	assert.True(t, hosts(t, 0))
}
