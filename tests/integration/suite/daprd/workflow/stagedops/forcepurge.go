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
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/dapr/dapr/tests/integration/framework/process/workflow/stagedops"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
)

func init() {
	suite.Register(new(forcepurge))
}

// forcepurge force purges a running workflow while no worker is connected.
// A force purge deletes the state and the reminders directly, without the
// workflow actor.
//   - off: works; the instance and its reminders are gone.
//   - on:  works; the instance and its reminders are gone.
type forcepurge struct{ stagedops.Modes }

func (r *forcepurge) Run(t *testing.T, ctx context.Context) {
	r.RunModes(t, ctx, func(t *testing.T, m stagedops.Mode) {
		// The modes share one scheduler: the ID is per mode, so the job
		// check below sees only this mode's reminders.
		id := api.InstanceID("forcepurge-" + m.Name)
		m.StartWith(t, ctx, 0, stagedops.WfEvent, stagedops.StatusRunning, id)
		mgmt := m.Mgmt(t, ctx, 0)

		got := m.Call(ctx, func(ctx context.Context) error {
			return mgmt.PurgeWorkflowState(ctx, id, api.WithForcePurge(true))
		})
		assert.Equal(t, stagedops.Accepted, got)
		assert.Equal(t, stagedops.StatusNotFound, stagedops.StatusOf(ctx, mgmt, id))

		jobs := m.WF.Scheduler().JobKeysLookup(t, ctx)
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Empty(c, jobs(c, string(id)))
		}, time.Second*10, time.Millisecond*100)
	})
}
