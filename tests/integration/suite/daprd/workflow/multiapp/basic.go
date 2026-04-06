package multiapp

import (
	"context"
	"fmt"
	"testing"
	"time"

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
	"github.com/dapr/kit/logger"
)

func init() {
	suite.Register(new(basic))
}

type basic struct {
	daprd1 *daprd.Daprd
	daprd2 *daprd.Daprd
	place  *placement.Placement
	sched  *scheduler.Scheduler
	log    logger.Logger
}

func (b *basic) Setup(t *testing.T) []framework.Option {
	b.log = logger.NewLogger("workflow-test")
	b.place = placement.New(t)
	b.sched = scheduler.New(t)
	db := sqlite.New(t,
		sqlite.WithActorStateStore(true),
		sqlite.WithMetadata("busyTimeout", "10s"),
		sqlite.WithMetadata("disableWAL", "true"),
	)

	b.daprd1 = daprd.New(t,
		daprd.WithResourceFiles(db.GetComponent(t)),
		daprd.WithPlacementAddresses(b.place.Address()),
		daprd.WithScheduler(b.sched),
		daprd.WithAppID("app1"),
		daprd.WithLogLevel("debug"),
	)
	b.daprd2 = daprd.New(t,
		daprd.WithResourceFiles(db.GetComponent(t)),
		daprd.WithPlacementAddresses(b.place.Address()),
		daprd.WithScheduler(b.sched),
		daprd.WithAppID("app2"),
		daprd.WithLogLevel("debug"),
	)

	return []framework.Option{
		framework.WithProcesses(b.place, b.sched, db, b.daprd1, b.daprd2),
	}
}

func (b *basic) Run(t *testing.T, ctx context.Context) {
	b.sched.WaitUntilRunning(t, ctx)
	b.place.WaitUntilRunning(t, ctx)
	b.daprd1.WaitUntilRunning(t, ctx)
	b.daprd2.WaitUntilRunning(t, ctx)
	b.log.Info("All processes are running")
	// Create registries for each app
	registry1 := task.NewTaskRegistry()
	registry2 := task.NewTaskRegistry()

	// Add orchestrator to app1
	registry1.AddOrchestratorN("SimpleWorkflow", func(ctx *task.OrchestrationContext) (any, error) {
		b.log.Debugf("Starting SimpleWorkflow in app1 with instance ID: %s, app ID: %+v", ctx.ID, ctx)

		var input string
		if err := ctx.GetInput(&input); err != nil {
			b.log.Errorf("Failed to get input in app1: %v", err)
			return nil, fmt.Errorf("failed to get input in app1: %w", err)
		}
		b.log.Debugf("App1 received input: %s", input)

		// Call sub-orchestrator in app2
		b.log.Info("Calling ProcessData sub-orchestrator in app2")
		app2ID := b.daprd2.AppID()
		b.log.Debugf("Using app2 ID: %s", app2ID)
		var output string
		err := ctx.CallSubOrchestrator("ProcessData",
			task.WithSubOrchestratorInput(input),
			/*task.WithSubOrchestratorAppID(app2ID)*/).Await(&output)
		if err != nil {
			b.log.Errorf("Failed to execute sub-orchestrator in app2: %v", err)
			return nil, fmt.Errorf("failed to execute activity in app2: %w", err)
		}
		b.log.Debugf("Received response from app2: %s", output)

		result := "Workflow completed: " + output
		b.log.Debugf("Completing workflow with result: %s", result)
		return result, nil
	})

	// Add orchestrator to app2
	registry2.AddOrchestratorN("ProcessData", func(ctx *task.OrchestrationContext) (any, error) {
		b.log.Debugf("Starting ProcessData in app2 with instance ID: %s, app ID: %+v", ctx.ID, ctx)

		var input string
		if err := ctx.GetInput(&input); err != nil {
			b.log.Errorf("Failed to get input in app2: %v", err)
			return nil, fmt.Errorf("failed to get input in app2: %w", err)
		}
		b.log.Debugf("App2 received input: %s", input)

		result := "[App2] Processed: " + input
		b.log.Debugf("App2 returning result: %s", result)
		return result, nil
	})

	// Start workflow listeners for each app
	b.log.Info("Initializing workflow clients")
	client1 := client.NewTaskHubGrpcClient(b.daprd1.GRPCConn(t, ctx), b.log)
	client2 := client.NewTaskHubGrpcClient(b.daprd2.GRPCConn(t, ctx), b.log)

	b.log.Info("Starting workflow listeners")
	require.NoError(t, client1.StartWorkItemListener(ctx, registry1))
	require.NoError(t, client2.StartWorkItemListener(ctx, registry2))

	// Add a delay to ensure everything is registered
	b.log.Info("Waiting for workflow engine initialization")
	time.Sleep(5 * time.Second)

	// Start the workflow
	b.log.Info("Starting the workflow")
	id, err := client1.ScheduleNewOrchestration(ctx, "SimpleWorkflow", api.WithRawInput(wrapperspb.String("test-data")))
	require.NoError(t, err)
	b.log.Debugf("Started workflow with ID: %s", id)

	// Wait for completion with timeout
	b.log.Info("Waiting for workflow completion")
	waitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	metadata, err := client1.WaitForOrchestrationCompletion(waitCtx, id, api.WithFetchPayloads(true))
	require.NoError(t, err)

	assert.True(t, api.OrchestrationMetadataIsComplete(metadata))
	b.log.Debugf("Workflow completed with status: %s", metadata.RuntimeStatus)

	assert.Equal(t, `"Workflow completed: [App2] Processed: test-data"`, metadata.GetOutput().GetValue())
	b.log.Info("Test completed successfully")
}
