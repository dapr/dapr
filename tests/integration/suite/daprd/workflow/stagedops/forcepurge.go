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
type forcepurge struct{ modes }

func (r *forcepurge) Run(t *testing.T, ctx context.Context) {
	r.run(t, ctx, func(t *testing.T, m mode) {
		// The modes share one scheduler: the ID is per mode, so the job
		// check below sees only this mode's reminders.
		id := api.InstanceID("forcepurge-" + m.name)
		m.startWith(t, ctx, 0, wfEvent, statusRunning, id)
		mgmt := m.mgmt(t, ctx, 0)

		got := m.call(ctx, func(ctx context.Context) error {
			return mgmt.PurgeWorkflowState(ctx, id, api.WithForcePurge(true))
		})
		assert.Equal(t, accepted, got)
		assert.Equal(t, statusNotFound, statusOf(ctx, mgmt, id))

		jobs := m.wf.Scheduler().JobKeysLookup(t, ctx)
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Empty(c, jobs(c, string(id)))
		}, time.Second*10, time.Millisecond*100)
	})
}
