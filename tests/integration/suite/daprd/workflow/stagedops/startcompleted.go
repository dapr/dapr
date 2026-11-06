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

package stagedops

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
)

func init() {
	suite.Register(new(startcompletedoff))
	suite.Register(new(startcompletedon))
}

type (
	startcompletedoff struct{ startcompleted }
	startcompletedon  struct{ startcompleted }
)

func (s *startcompletedon) Setup(t *testing.T) []framework.Option {
	s.staged = true
	return s.startcompleted.Setup(t)
}

// startcompleted starts a workflow again under the ID of an instance that has
// completed, while no worker is connected.
//   - off: fails, no worker connected. The old instance stays COMPLETED.
//   - on:  accepted. The new run replaces the old instance, PENDING until a
//     worker connects, then it runs.
type startcompleted struct{ mode }

func (s *startcompleted) Run(t *testing.T, ctx context.Context) {
	s.wf.WaitUntilRunning(t, ctx)
	addWorkflows(t, s.wf.Registry())

	const id = api.InstanceID("startcompleted")
	startWith(t, ctx, s.wf, 0, s.staged, wfDone, statusCompleted, id)
	mgmt := s.wf.ManagementClient(t, ctx)

	got := call(ctx, s.staged, func(ctx context.Context) error {
		_, err := mgmt.ScheduleNewWorkflow(ctx, wfDone, api.WithInstanceID(id))
		return err
	})
	assert.Equal(t, want(s.staged, noWorker, accepted), got)
	assert.Equal(t, want(s.staged, statusCompleted, statusPending), statusOf(ctx, mgmt, id))

	s.wf.ConnectWorker(t, ctx, s.wf.Registry())
	s.wf.WaitForConnectedWorkers(t, ctx, 1)
	waitStatus(t, ctx, mgmt, id, statusCompleted)
}
