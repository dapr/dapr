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

	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
)

func init() {
	suite.Register(new(purge))
}

// purge purges a completed workflow while no worker is connected.
//   - off: fails, no worker connected. The instance is kept.
//   - on:  works at once.
type purge struct{ modes }

func (r *purge) Run(t *testing.T, ctx context.Context) {
	r.run(t, ctx, func(t *testing.T, m mode) {
		const id = api.InstanceID("purge")
		m.startWith(t, ctx, 0, wfDone, statusCompleted, id)
		mgmt := m.mgmt(t, ctx, 0)

		got := m.call(ctx, func(ctx context.Context) error {
			return mgmt.PurgeWorkflowState(ctx, id)
		})
		assert.Equal(t, want(m.staged, noWorker, accepted), got)
		assert.Equal(t, want(m.staged, statusCompleted, statusNotFound), statusOf(ctx, mgmt, id))
	})
}
