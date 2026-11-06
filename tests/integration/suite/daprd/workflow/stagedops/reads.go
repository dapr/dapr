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
	suite.Register(new(reads))
}

// reads reads the status and the history of a completed workflow, and lists
// the instance IDs, while no worker is connected.
//   - off: all work.
//   - on:  all work.
type reads struct{ stagedops.Modes }

func (r *reads) Run(t *testing.T, ctx context.Context) {
	r.RunModes(t, ctx, func(t *testing.T, m stagedops.Mode) {
		const id = api.InstanceID("reads")
		m.StartWith(t, ctx, 0, stagedops.WfDone, stagedops.StatusCompleted, id)
		mgmt := m.Mgmt(t, ctx, 0)

		assert.Equal(t, stagedops.Accepted, m.Call(ctx, func(ctx context.Context) error {
			_, err := mgmt.FetchWorkflowMetadata(ctx, id)
			return err
		}), "get status")
		assert.Equal(t, stagedops.Accepted, m.Call(ctx, func(ctx context.Context) error {
			_, err := mgmt.GetInstanceHistory(ctx, id)
			return err
		}), "get history")

		var ids []string
		assert.Equal(t, stagedops.Accepted, m.Call(ctx, func(ctx context.Context) error {
			resp, err := mgmt.ListInstanceIDs(ctx)
			ids = resp.GetInstanceIds()
			return err
		}), "list instance IDs")
		assert.Equal(t, []string{string(id)}, ids)
	})
}
