package force

import (
	"context"
	"testing"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/backend"
	dworkflow "github.com/dapr/durabletask-go/workflow"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

func init() {
	suite.Register(new(missingInbox))
}

type missingInbox struct {
	workflow *workflow.Workflow
}

func (m *missingInbox) Setup(t *testing.T) []framework.Option {
	m.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(m.workflow),
	}
}

func (m *missingInbox) Run(t *testing.T, ctx context.Context) {
	m.workflow.WaitUntilRunning(t, ctx)

	reg := dworkflow.NewRegistry()

	reg.AddWorkflowN("missing-inbox-purge", func(ctx *dworkflow.WorkflowContext) (any, error) {
		return nil, nil
	})

	client := m.workflow.WorkflowClient(t, ctx)
	require.NoError(t, client.StartWorker(ctx, reg))

	id, err := client.ScheduleWorkflow(ctx, "missing-inbox-purge")
	require.NoError(t, err)

	_, err = client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)

	db := m.workflow.DB()

	// Confirm the workflow has no inbox records before corrupting metadata.
	require.Empty(t, db.ReadStateValues(t, ctx, id, "inbox"))

	// Simulate inconsistent metadata: it declares one inbox entry,
	// but the corresponding inbox-000000 record does not exist.
	key, raw := db.ReadStateValue(t, ctx, id, "metadata")

	var metadata backend.BackendWorkflowStateMetadata
	require.NoError(t, proto.Unmarshal(raw, &metadata))

	metadata.InboxLength = 1

	updated, err := proto.Marshal(&metadata)
	require.NoError(t, err)

	db.WriteStateValue(t, ctx, key, updated)

	// Force purge should succeed despite the missing inbox entry.
	require.NoError(t,
		client.PurgeWorkflowState(ctx, id, dworkflow.WithForcePurge(true)),
	)

	var count int
	err = db.GetConnection(t).QueryRowContext(ctx,
		"SELECT COUNT(*) FROM "+db.TableName(),
	).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 0, count)
}
