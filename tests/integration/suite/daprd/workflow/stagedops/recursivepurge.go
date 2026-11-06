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

	"github.com/dapr/dapr/tests/integration/framework/process/workflow/stagedops"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
)

func init() {
	suite.Register(new(recursivepurge))
}

// recursivepurge purges a completed parent workflow and its completed child
// while no worker is connected.
//   - off: fails, no worker connected. Both instances are kept.
//   - on:  works at once; both are gone.
type recursivepurge struct{ stagedops.Modes }

func (r *recursivepurge) Run(t *testing.T, ctx context.Context) {
	r.RunModes(t, ctx, func(t *testing.T, m stagedops.Mode) {
		const (
			id    = api.InstanceID("recursivepurge")
			child = id + "-child"
		)
		m.StartWith(t, ctx, 0, stagedops.WfParent, stagedops.StatusCompleted, id)
		mgmt := m.Mgmt(t, ctx, 0)
		assert.Equal(t, stagedops.StatusCompleted, stagedops.StatusOf(ctx, mgmt, child))

		got := m.Call(ctx, func(ctx context.Context) error {
			return mgmt.PurgeWorkflowState(ctx, id, api.WithRecursivePurge(true))
		})
		assert.Equal(t, stagedops.Want(m.Staged, stagedops.NoWorker, stagedops.Accepted), got)
		assert.Equal(t, stagedops.Want(m.Staged, stagedops.StatusCompleted, stagedops.StatusNotFound), stagedops.StatusOf(ctx, mgmt, id))
		assert.Equal(t, stagedops.Want(m.Staged, stagedops.StatusCompleted, stagedops.StatusNotFound), stagedops.StatusOf(ctx, mgmt, child))
	})
}
