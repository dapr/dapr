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
package perworkflow

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
)

// policy builds a Configuration from the body of a stateRetentionPolicy,
// given already indented by six spaces.
func policy(t *testing.T, retention string) daprd.Option {
	t.Helper()
	return daprd.WithConfigManifests(t, `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: wfpolicy
spec:
  workflow:
    stateRetentionPolicy:
`+retention)
}

// counter returns the number of stored state rows of one workflow instance.
type counter func(require.TestingT, string) int

func rowCounter(t *testing.T, ctx context.Context, w *workflow.Workflow) counter {
	t.Helper()
	db := w.DB().GetConnection(t)
	table := w.DB().TableName()
	return func(t require.TestingT, id string) int {
		var n int
		require.NoError(t, db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table+" WHERE key LIKE ?", "%||"+id+"||%").Scan(&n))
		return n
	}
}

func waitPurged(t *testing.T, rows counter, id, msg string) {
	t.Helper()
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, 0, rows(c, id))
	}, time.Second*20, time.Millisecond*10, msg)
}
