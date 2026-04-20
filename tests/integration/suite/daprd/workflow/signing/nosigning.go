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

package signing

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/sqlite"
	fworkflow "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	dworkflow "github.com/dapr/durabletask-go/workflow"
)

func init() {
	suite.Register(new(nosigning))
}

// nosigning verifies that a workflow without mTLS and without signing enabled
// produces history entries but no signatures or signing certificates.
type nosigning struct {
	daprd *daprd.Daprd
	db    *sqlite.SQLite
}

func (n *nosigning) Setup(t *testing.T) []framework.Option {
	n.db = sqlite.New(t,
		sqlite.WithActorStateStore(true),
		sqlite.WithCreateStateTables(),
	)
	place := placement.New(t)
	sched := scheduler.New(t)

	n.daprd = daprd.New(t,
		daprd.WithPlacement(place),
		daprd.WithScheduler(sched),
		daprd.WithResourceFiles(n.db.GetComponent(t)),
	)

	return []framework.Option{
		framework.WithProcesses(n.db, place, sched, n.daprd),
	}
}

func (n *nosigning) Run(t *testing.T, ctx context.Context) {
	n.daprd.WaitUntilRunning(t, ctx)

	reg := dworkflow.NewRegistry()
	reg.AddWorkflowN("sign-no-mtls", func(ctx *dworkflow.WorkflowContext) (any, error) {
		return "", nil
	})

	client := dworkflow.NewClient(n.daprd.GRPCConn(t, ctx))
	require.NoError(t, client.StartWorker(ctx, reg))

	id, err := client.ScheduleWorkflow(ctx, "sign-no-mtls")
	require.NoError(t, err)

	_, err = client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)

	assert.Equal(t, 0, fworkflow.SignatureCount(t, ctx, n.db, id))
	assert.Equal(t, 0, fworkflow.CertificateCount(t, ctx, n.db, id))
	assert.Positive(t, n.db.CountStateKeys(t, ctx, "history"))
}
