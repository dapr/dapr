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
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
)

func init() {
	suite.Register(new(retention))
}

// retentionPeriod is how long a completed workflow is kept.
const retentionPeriod = time.Second * 6

// retention lets the retention period of a completed workflow pass while no
// worker is connected.
//   - off: the workflow is not purged while no worker is connected; the
//     scheduler holds the retention reminder, and the workflow is purged
//     once a worker connects.
//   - on:  the same.
type retention struct{ modes }

func (r *retention) Setup(t *testing.T) []framework.Option {
	return r.setup(t, 1, func(bool, int) []daprd.Option {
		return []daprd.Option{daprd.WithConfigManifests(t, `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: wfpolicy
spec:
  workflow:
    stateRetentionPolicy:
      completed: "`+retentionPeriod.String()+`"
`)}
	})
}

func (r *retention) Run(t *testing.T, ctx context.Context) {
	r.run(t, ctx, func(t *testing.T, m mode) {
		const id = api.InstanceID("retention")
		m.startWith(t, ctx, 0, wfDone, statusCompleted, id)
		completed := time.Now()
		mgmt := m.mgmt(t, ctx, 0)

		// startWith returns once the worker is gone; that must be before the
		// retention reminder is due, or this checks nothing.
		require.Less(t, time.Since(completed), retentionPeriod/2)

		// Past the retention period, with a margin for the reminder to fire.
		time.Sleep(time.Until(completed.Add(retentionPeriod + time.Second*5)))
		assert.Equal(t, statusCompleted, statusOf(ctx, mgmt, id))

		m.connect(t, ctx, 0)
		waitStatus(t, ctx, mgmt, id, statusNotFound)
	})
}
