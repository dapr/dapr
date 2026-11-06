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
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
)

func init() {
	suite.Register(new(resumeoff))
	suite.Register(new(resumeon))
}

type (
	resumeoff struct{ resume }
	resumeon  struct{ resume }
)

func (r *resumeon) Setup(t *testing.T) []framework.Option {
	r.staged = true
	return r.resume.Setup(t)
}

// resume resumes a suspended workflow while no worker is connected.
//   - off: fails, no worker connected.
//   - on:  accepted. The instance stays SUSPENDED until a worker connects,
//     then it is RUNNING.
type resume struct{ mode }

func (r *resume) Run(t *testing.T, ctx context.Context) {
	r.wf.WaitUntilRunning(t, ctx)
	addWorkflows(t, r.wf.Registry())

	const id = api.InstanceID("resume")
	worker := r.wf.ConnectWorker(t, ctx, r.wf.Registry())
	r.wf.WaitForConnectedWorkers(t, ctx, 1)
	mgmt := r.wf.ManagementClient(t, ctx)
	_, err := mgmt.ScheduleNewWorkflow(ctx, wfEvent, api.WithInstanceID(id))
	require.NoError(t, err)
	waitStatus(t, ctx, mgmt, id, statusRunning)
	require.NoError(t, mgmt.SuspendWorkflow(ctx, id, "suspend"))
	waitStatus(t, ctx, mgmt, id, statusSuspended)
	worker.Disconnect(t)
	waitNoWorker(t, ctx, r.wf, 0, r.staged)

	got := call(ctx, r.staged, func(ctx context.Context) error {
		return mgmt.ResumeWorkflow(ctx, id, "resume")
	})
	assert.Equal(t, want(r.staged, noWorker, accepted), got)
	assert.Equal(t, statusSuspended, statusOf(ctx, mgmt, id))

	if !r.staged {
		return
	}

	r.wf.ConnectWorker(t, ctx, r.wf.Registry())
	r.wf.WaitForConnectedWorkers(t, ctx, 1)
	waitStatus(t, ctx, mgmt, id, statusRunning)
}
