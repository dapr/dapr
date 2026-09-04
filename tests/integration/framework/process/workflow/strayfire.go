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

package workflow

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// StrayFire schedules a stray new-event reminder against the workflow actor
// hosted by daprd index, driving its empty-inbox path, and waits until the
// scheduler has delivered it.
func (w *Workflow) StrayFire(t *testing.T, ctx context.Context, index int, instanceID string, mtls bool) {
	t.Helper()
	sched := w.Scheduler()
	appID := w.DaprN(index).AppID()
	job := sched.JobNowActor("new-event-stray-"+instanceID, "default", appID, w.WorkflowActorType(index), instanceID)
	var err error
	if mtls {
		_, err = sched.ClientMTLS(t, ctx, appID).ScheduleJob(ctx, job)
	} else {
		_, err = sched.Client(t, ctx).ScheduleJob(ctx, job)
	}
	require.NoError(t, err)
	sched.WaitJobKeyCount(t, ctx, "new-event-stray-"+instanceID, func(n int) bool { return n == 0 })
}
