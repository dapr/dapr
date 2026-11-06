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
	suite.Register(new(raiseoff))
	suite.Register(new(raiseon))
}

type (
	raiseoff struct{ raise }
	raiseon  struct{ raise }
)

func (r *raiseon) Setup(t *testing.T) []framework.Option {
	r.staged = true
	return r.raise.Setup(t)
}

// raise raises an event to a running workflow while no worker is connected.
//   - off: fails, no worker connected. The event is not kept.
//   - on:  accepted. The workflow gets the event once a worker connects.
type raise struct{ mode }

func (r *raise) Run(t *testing.T, ctx context.Context) {
	r.wf.WaitUntilRunning(t, ctx)
	addWorkflows(t, r.wf.Registry())

	const id = api.InstanceID("raise")
	startWith(t, ctx, r.wf, 0, r.staged, wfEvent, statusRunning, id)
	mgmt := r.wf.ManagementClient(t, ctx)

	got := call(ctx, r.staged, func(ctx context.Context) error {
		return mgmt.RaiseEvent(ctx, id, evGo, api.WithEventPayload("raised"))
	})
	assert.Equal(t, want(r.staged, noWorker, accepted), got)
	assert.Equal(t, statusRunning, statusOf(ctx, mgmt, id))

	if !r.staged {
		return
	}

	r.wf.ConnectWorker(t, ctx, r.wf.Registry())
	r.wf.WaitForConnectedWorkers(t, ctx, 1)
	waitStatus(t, ctx, mgmt, id, statusCompleted)
	meta, err := mgmt.FetchWorkflowMetadata(ctx, id)
	require.NoError(t, err)
	assert.JSONEq(t, `"raised"`, meta.GetOutput().GetValue())
}
