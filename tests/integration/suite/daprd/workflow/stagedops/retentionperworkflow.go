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
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow/stagedops"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
)

func init() {
	suite.Register(new(retentionperworkflow))
}

// retentionperworkflow is retention with the TTL set by a per-workflow entry
// for WfDone instead of the app-wide policy, which keeps it for an hour.
type retentionperworkflow struct{ stagedops.Modes }

func (r *retentionperworkflow) Setup(t *testing.T) []framework.Option {
	return r.SetupN(t, 1, func(bool, int) []daprd.Option {
		return []daprd.Option{daprd.WithConfigManifests(t, `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: wfpolicy
spec:
  workflow:
    stateRetentionPolicy:
      completed: "1h"
      workflows:
        - name: `+stagedops.WfDone+`
          completed: "`+retentionPeriod.String()+`"
`)}
	})
}

func (r *retentionperworkflow) Run(t *testing.T, ctx context.Context) {
	r.RunModes(t, ctx, func(t *testing.T, m stagedops.Mode) {
		const id = api.InstanceID("retention")
		before := time.Now()
		m.StartWith(t, ctx, 0, stagedops.WfDone, stagedops.StatusCompleted, id)
		completed := time.Now()
		mgmt := m.Mgmt(t, ctx, 0)

		// StartWith returns once the worker is gone; that must be before the
		// retention reminder is due, or this checks nothing. The workflow
		// completes no earlier than before.
		require.Less(t, completed.Sub(before), retentionPeriod/2)

		// Past the retention period, with a margin for the reminder to fire.
		time.Sleep(time.Until(completed.Add(retentionPeriod + time.Second*5)))
		assert.Equal(t, stagedops.StatusCompleted, stagedops.StatusOf(ctx, mgmt, id))

		m.Connect(t, ctx, 0)
		stagedops.WaitStatus(t, ctx, mgmt, id, stagedops.StatusNotFound)
	})
}
