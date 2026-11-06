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
	suite.Register(new(purgeoff))
	suite.Register(new(purgeon))
}

type (
	purgeoff struct{ purge }
	purgeon  struct{ purge }
)

func (p *purgeon) Setup(t *testing.T) []framework.Option {
	p.staged = true
	return p.purge.Setup(t)
}

// purge purges a completed workflow while no worker is connected.
//   - off: fails, no worker connected. The instance is kept.
//   - on:  works at once.
type purge struct{ mode }

func (r *purge) Run(t *testing.T, ctx context.Context) {
	r.wf.WaitUntilRunning(t, ctx)
	addWorkflows(t, r.wf.Registry())

	const id = api.InstanceID("purge")
	startWith(t, ctx, r.wf, 0, r.staged, wfDone, statusCompleted, id)
	mgmt := r.wf.ManagementClient(t, ctx)

	got := call(ctx, r.staged, func(ctx context.Context) error {
		return mgmt.PurgeWorkflowState(ctx, id)
	})
	assert.Equal(t, want(r.staged, noWorker, accepted), got)
	assert.Equal(t, want(r.staged, statusCompleted, statusNotFound), statusOf(ctx, mgmt, id))
}
