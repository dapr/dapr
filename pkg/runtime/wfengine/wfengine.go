/*
Copyright 2023 The Dapr Authors
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

package wfengine

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/grpc"

	"github.com/dapr/components-contrib/workflows"
	"github.com/dapr/dapr/pkg/actors"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/orchestrator"
	"github.com/dapr/dapr/pkg/config"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	"github.com/dapr/dapr/pkg/runtime/mcp"
	"github.com/dapr/dapr/pkg/runtime/processor"
	backendactors "github.com/dapr/dapr/pkg/runtime/wfengine/backends/actors"
	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/durabletask-go/backend"
	dtclient "github.com/dapr/durabletask-go/client"
	spiffecontext "github.com/dapr/kit/crypto/spiffe/context"
	"github.com/dapr/kit/logger"
	"github.com/spiffe/go-spiffe/v2/svid/jwtsvid"
)

var (
	log             = logger.NewLogger("dapr.runtime.wfengine")
	wfBackendLogger = logger.NewLogger("dapr.wfengine.durabletask.backend")
)

type Interface interface {
	Run(context.Context) error
	RegisterGrpcServer(*grpc.Server)
	Client() workflows.Workflow
	RuntimeMetadata() *runtimev1pb.MetadataWorkflows

	ActivityActorType() string

	// ActivateMCPServers ensures workflow actors are registered,
	// so that built-in dapr.mcp.* orchestrations can be scheduled even when no user workflow client has connected via GetWorkItems.
	// It is called by the runtime after MCPServer manifests are loaded.
	ActivateMCPServers(ctx context.Context) error
}

type Options struct {
	AppID          string
	Namespace      string
	Actors         actors.Interface
	Spec           *config.WorkflowSpec
	BackendManager processor.WorkflowBackendManager
	Resiliency     resiliency.Provider
	EventSink      orchestrator.EventSink
	ComponentStore *compstore.ComponentStore
	// Security is optional. When set,
	// SPIFFE JWT SVID injection is enabled for MCPServer resources that configure auth.spiffe.
	Security security.Handler

	EnableClusteredDeployment       bool
	WorkflowsRemoteActivityReminder bool
}

type engine struct {
	appID             string
	namespace         string
	actors            actors.Interface
	getWorkItemsCount *atomic.Int32

	worker   backend.TaskHubWorker
	backend  *backendactors.Actors
	client   workflows.Workflow
	executor backend.Executor

	registerGrpcServerFn func(grpcServer grpc.ServiceRegistrar)

	// mcpOpts configures the built-in MCP registry.
	mcpOpts   mcp.ExecutorOptions
	mcpClient *dtclient.InProcessClient
}

func New(opts Options) Interface {
	var retPolicy *config.WorkflowStateRetentionPolicy
	if opts.Spec != nil {
		retPolicy = opts.Spec.StateRetentionPolicy
	}

	// If no backend was initialized by the manager, create a backend backed by actors
	abackend := backendactors.New(backendactors.Options{
		AppID:           opts.AppID,
		Namespace:       opts.Namespace,
		Actors:          opts.Actors,
		Resiliency:      opts.Resiliency,
		EventSink:       opts.EventSink,
		ComponentStore:  opts.ComponentStore,
		RetentionPolicy: retPolicy,

		EnableClusteredDeployment:       opts.EnableClusteredDeployment,
		WorkflowsRemoteActivityReminder: opts.WorkflowsRemoteActivityReminder,
	})

	var (
		getWorkItemsCount atomic.Int32
		lock              sync.Mutex
	)

	grpcExec, registerGrpcServerFn := backend.NewGrpcExecutor(abackend, log,
		backend.WithOnGetWorkItemsConnectionCallback(func(ctx context.Context) error {
			lock.Lock()
			defer lock.Unlock()

			if getWorkItemsCount.Add(1) == 1 {
				log.Debug("Registering workflow actors")
				return abackend.RegisterActors(ctx)
			}

			return nil
		}),
		backend.WithOnGetWorkItemsDisconnectCallback(func(ctx context.Context) error {
			lock.Lock()
			defer lock.Unlock()

			if ctx.Err() != nil {
				ctx = context.Background()
			}

			if getWorkItemsCount.Add(-1) == 0 {
				log.Debug("Unregistering workflow actors")
				return abackend.UnRegisterActors(ctx)
			}

			return nil
		}),
		backend.WithStreamSendTimeout(time.Second*10),
	)

	// TODO: handle somewhere that users cannot use a managed workflow name themselves.

	// MCP orchestrations are routed to an in-process sink registered on the
	// gRPC executor by ActivateMCPServers. Until then, the gRPC executor
	// operates exactly as before — all work items go to the external SDK.
	mcpOpts := mcp.ExecutorOptions{
		Store:   opts.ComponentStore,
		Secrets: mcp.NewCompstoreSecretGetter(opts.ComponentStore),
		JWT:     newSPIFFEJWTFetcher(opts.Security),
	}
	executor := grpcExec

	var topts []backend.NewTaskWorkerOptions
	if opts.Spec.GetMaxConcurrentWorkflowInvocations() != nil {
		topts = []backend.NewTaskWorkerOptions{
			backend.WithMaxParallelism(*opts.Spec.GetMaxConcurrentWorkflowInvocations()),
		}
	}

	// There are separate "workers" for executing orchestrations (workflows) and activities
	oworker := backend.NewWorkflowWorker(backend.WorkflowWorkerOptions{
		Backend:  abackend,
		Executor: executor,
		Logger:   wfBackendLogger,
		AppID:    opts.AppID,
	}, topts...)

	topts = nil
	if opts.Spec.GetMaxConcurrentActivityInvocations() != nil {
		topts = []backend.NewTaskWorkerOptions{
			backend.WithMaxParallelism(*opts.Spec.GetMaxConcurrentActivityInvocations()),
		}
	}

	aworker := backend.NewActivityTaskWorker(
		abackend,
		executor,
		wfBackendLogger,
		topts...,
	)
	worker := backend.NewTaskHubWorker(abackend, oworker, aworker, wfBackendLogger)

	return &engine{
		appID:                opts.AppID,
		namespace:            opts.Namespace,
		actors:               opts.Actors,
		worker:               worker,
		backend:              abackend,
		executor:             grpcExec,
		registerGrpcServerFn: registerGrpcServerFn,
		getWorkItemsCount:    &getWorkItemsCount,
		mcpOpts:              mcpOpts,
		client: &client{
			logger: wfBackendLogger,
			client: backend.NewTaskHubClient(abackend),
		},
	}
}

func (wfe *engine) RegisterGrpcServer(server *grpc.Server) {
	wfe.registerGrpcServerFn(server)
}

func (wfe *engine) Run(ctx context.Context) error {
	_, err := wfe.actors.Router(ctx)
	if err != nil {
		<-ctx.Done()
		return ctx.Err()
	}

	// Start the Durable Task worker, which will allow workflows to be scheduled and execute.
	if err := wfe.worker.Start(ctx); err != nil {
		return fmt.Errorf("failed to start workflow engine: %w", err)
	}

	log.Info("Workflow engine started")
	<-ctx.Done()

	if err := wfe.worker.Shutdown(context.Background()); err != nil {
		return fmt.Errorf("failed to shutdown the workflow worker: %w", err)
	}

	log.Info("Workflow engine stopped")

	return nil
}

func (wfe *engine) Client() workflows.Workflow {
	return wfe.client
}

func (wfe *engine) ActivityActorType() string {
	return wfe.backend.ActivityActorType()
}

func (wfe *engine) RuntimeMetadata() *runtimev1pb.MetadataWorkflows {
	return &runtimev1pb.MetadataWorkflows{
		ConnectedWorkers: wfe.getWorkItemsCount.Load(),
	}
}

// ActivateMCPServers registers an in-process work-item sink and worker for
// the built-in dapr.mcp.* orchestrations and dapr-mcp-* activities, then
// ensures workflow actors are registered with the actor runtime. This
// allows MCP workflows to be scheduled even when no user app has connected
// via GetWorkItems.
func (wfe *engine) ActivateMCPServers(ctx context.Context) error {
	log.Debug("Activating workflow actors for MCP server orchestrations")

	// Build the task registry containing the MCP wildcard orchestrator and
	// the two MCP transport activities.
	registry := mcp.NewBuiltinRegistry(wfe.mcpOpts)

	// Create the in-process client that will serve as the work-item sink.
	ipClient := dtclient.NewTaskHubInProcessClient(wfe.backend, wfBackendLogger)

	// Register the client as a sink for MCP-prefixed workflows and
	// activities. The grpcExecutor routes matching work items to this
	// client instead of the external gRPC stream.
	registrar, ok := wfe.executor.(backend.SinkRegistrar)
	if !ok {
		return fmt.Errorf("executor does not implement SinkRegistrar; cannot register MCP in-process sink")
	}
	if err := registrar.RegisterSink(backend.SinkOptions{
		WorkflowNamePrefix: mcp.WorkflowNamePrefix, // "dapr.mcp."
		ActivityNamePrefix: mcp.ActivityNamePrefix, // "dapr-mcp-"
	}, ipClient); err != nil {
		log.Warnf("MCP sink registration returned: %s (may be duplicate; continuing)", err)
	}

	// Start the background processor that drains the sink and dispatches to the task registry.
	if err := ipClient.StartWorkItemListener(ctx, registry); err != nil {
		return fmt.Errorf("failed to start MCP in-process work-item listener: %w", err)
	}

	wfe.mcpClient = ipClient

	return wfe.backend.RegisterActors(ctx)
}

// spiffeJWTFetcher implements mcp.JWTFetcher using the runtime's security handler.
// It injects a SPIFFE SVID context and fetches a JWT SVID for the given audience.
// When sec is nil (security disabled), FetchJWT returns an error.
type spiffeJWTFetcher struct {
	sec security.Handler
}

func newSPIFFEJWTFetcher(sec security.Handler) *spiffeJWTFetcher {
	return &spiffeJWTFetcher{sec: sec}
}

func (f *spiffeJWTFetcher) FetchJWT(ctx context.Context, audience string) (string, error) {
	if f.sec == nil {
		return "", fmt.Errorf("SPIFFE JWT auth is configured but no security handler is available")
	}
	ctxWithSVID := f.sec.WithSVIDContext(ctx)
	jwtSource, ok := spiffecontext.JWTFrom(ctxWithSVID)
	if !ok {
		return "", fmt.Errorf("SPIFFE JWT source not available in context")
	}
	svid, err := jwtSource.FetchJWTSVID(ctxWithSVID, jwtsvid.Params{Audience: audience})
	if err != nil {
		return "", fmt.Errorf("failed to fetch SPIFFE JWT SVID: %w", err)
	}
	return svid.Marshal(), nil
}
