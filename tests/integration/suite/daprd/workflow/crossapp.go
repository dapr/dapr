package workflow

import (
	"context"
	"testing"

	"github.com/dapr/durabletask-go/backend"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/sqlite"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(crossapp))
}

type crossapp struct {
	daprds []*daprd.Daprd
}

func (m *crossapp) Setup(t *testing.T) []framework.Option {
	place := placement.New(t)
	sched := scheduler.New(t)
	db := sqlite.New(t,
		sqlite.WithActorStateStore(true),
		sqlite.WithMetadata("busyTimeout", "10s"),
		sqlite.WithMetadata("disableWAL", "true"),
	)

	m.daprds = make([]*daprd.Daprd, 3)
	m.daprds[0] = daprd.New(t,
		daprd.WithResourceFiles(db.GetComponent(t)),
		daprd.WithPlacementAddresses(place.Address()),
		daprd.WithScheduler(sched),
		daprd.WithLogLevel("debug"),
	)
	m.daprds[1] = daprd.New(t,
		daprd.WithResourceFiles(db.GetComponent(t)),
		daprd.WithPlacementAddresses(place.Address()),
		daprd.WithScheduler(sched),
		//daprd.WithAppID(m.daprds[1].AppID()),
		daprd.WithLogLevel("debug"),
	)
	m.daprds[2] = daprd.New(t,
		daprd.WithResourceFiles(db.GetComponent(t)),
		daprd.WithPlacementAddresses(place.Address()),
		daprd.WithScheduler(sched),
		//daprd.WithAppID(m.daprds[2].AppID()),
		daprd.WithLogLevel("debug"),
	)

	return []framework.Option{
		framework.WithProcesses(place, sched, db, m.daprds[0], m.daprds[1], m.daprds[2]),
	}
}

func (c *crossapp) Run(t *testing.T, ctx context.Context) {
	c.daprds[0].WaitUntilRunning(t, ctx)
	c.daprds[1].WaitUntilRunning(t, ctx)
	c.daprds[2].WaitUntilRunning(t, ctx)

	// Create registries for each app
	registry1 := task.NewTaskRegistry()
	registry2 := task.NewTaskRegistry()
	registry3 := task.NewTaskRegistry()

	// Add orchestrators and activities to each registry
	registry1.AddOrchestratorN("CrossAppOrchestrator", func(ctx *task.OrchestrationContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, err
		}

		// Call sub-orchestrator in app2
		var output string
		err := ctx.CallSubOrchestrator("MiddleOrchestrator",
			task.WithSubOrchestratorInput(input),
			/*task.WithSubOrchestratorAppID(c.daprds[1].AppID())*/).Await(&output)
		if err != nil {
			return nil, err
		}

		return "Main completed: " + output, nil
	})

	registry2.AddOrchestratorN("MiddleOrchestrator", func(ctx *task.OrchestrationContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, err
		}

		// Call sub-orchestrator in app3
		var output string
		err := ctx.CallSubOrchestrator("FinalizeOrchestrator",
			task.WithSubOrchestratorInput(input),
			/*task.WithSubOrchestratorAppID(c.daprds[2].AppID())*/).Await(&output)
		if err != nil {
			return nil, err
		}

		return "Middle completed: " + output, nil
	})

	registry3.AddOrchestratorN("FinalizeOrchestrator", func(ctx *task.OrchestrationContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, err
		}

		// Process the data
		processed := "[App app3] Processed: " + input
		return "Finalized: " + processed, nil
	})

	// Add activity to each app
	registry1.AddActivityN("ProcessDataActivity", func(ctx task.ActivityContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, err
		}
		return "[App app1] Processed: " + input, nil
	})

	registry2.AddActivityN("ProcessDataActivity", func(ctx task.ActivityContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, err
		}
		return "[App app2] Processed: " + input, nil
	})

	registry3.AddActivityN("ProcessDataActivity", func(ctx task.ActivityContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, err
		}
		return "[App app3] Processed: " + input, nil
	})

	// Start workflow listeners for each app
	client1 := client.NewTaskHubGrpcClient(c.daprds[0].GRPCConn(t, ctx), backend.DefaultLogger())
	require.NoError(t, client1.StartWorkItemListener(ctx, registry1))

	client2 := client.NewTaskHubGrpcClient(c.daprds[1].GRPCConn(t, ctx), backend.DefaultLogger())
	require.NoError(t, client2.StartWorkItemListener(ctx, registry2))

	client3 := client.NewTaskHubGrpcClient(c.daprds[2].GRPCConn(t, ctx), backend.DefaultLogger())
	require.NoError(t, client3.StartWorkItemListener(ctx, registry3))

	// Start the workflow
	id, err := client1.ScheduleNewOrchestration(ctx, "CrossAppOrchestrator", api.WithRawInput(wrapperspb.String("data1")))
	require.NoError(t, err)

	// Wait for completion
	metadata, err := client1.WaitForOrchestrationCompletion(ctx, id, api.WithFetchPayloads(true))
	require.NoError(t, err)
	assert.True(t, api.OrchestrationMetadataIsComplete(metadata))
	assert.Equal(t, `"Main completed: Middle completed: Finalized: [App app3] Processed: [App app2] Processed: [App app1] Processed: data1"`, metadata.GetOutput().GetValue())
}
