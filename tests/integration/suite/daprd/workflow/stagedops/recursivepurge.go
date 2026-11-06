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
	suite.Register(new(recursivepurgeoff))
	suite.Register(new(recursivepurgeon))
}

type (
	recursivepurgeoff struct{ recursivepurge }
	recursivepurgeon  struct{ recursivepurge }
)

func (r *recursivepurgeon) Setup(t *testing.T) []framework.Option {
	r.staged = true
	return r.recursivepurge.Setup(t)
}

// recursivepurge purges a completed parent workflow and its completed child
// while no worker is connected.
//   - off: fails, no worker connected. Both instances are kept.
//   - on:  works at once; both are gone.
type recursivepurge struct{ mode }

func (r *recursivepurge) Run(t *testing.T, ctx context.Context) {
	r.wf.WaitUntilRunning(t, ctx)
	addWorkflows(t, r.wf.Registry())

	const (
		id    = api.InstanceID("recursivepurge")
		child = id + "-child"
	)
	startWith(t, ctx, r.wf, 0, r.staged, wfParent, statusCompleted, id)
	mgmt := r.wf.ManagementClient(t, ctx)
	assert.Equal(t, statusCompleted, statusOf(ctx, mgmt, child))

	got := call(ctx, r.staged, func(ctx context.Context) error {
		return mgmt.PurgeWorkflowState(ctx, id, api.WithRecursivePurge(true))
	})
	assert.Equal(t, want(r.staged, noWorker, accepted), got)
	assert.Equal(t, want(r.staged, statusCompleted, statusNotFound), statusOf(ctx, mgmt, id))
	assert.Equal(t, want(r.staged, statusCompleted, statusNotFound), statusOf(ctx, mgmt, child))
}
