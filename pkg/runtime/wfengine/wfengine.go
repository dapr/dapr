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
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/grpc"

	"github.com/dapr/components-contrib/workflows"
	workflowacl "github.com/dapr/dapr/pkg/acl/workflow"
	"github.com/dapr/dapr/pkg/actors"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/orchestrator"
	mcpserverapi "github.com/dapr/dapr/pkg/apis/mcpserver/v1alpha1"
	"github.com/dapr/dapr/pkg/config"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	"github.com/dapr/dapr/pkg/runtime/processor"
	backendactors "github.com/dapr/dapr/pkg/runtime/wfengine/backends/actors"
	"github.com/dapr/dapr/pkg/runtime/wfengine/inprocess"
	"github.com/dapr/dapr/pkg/runtime/wfengine/wfregistrar"
	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/kit/crypto/spiffe/signer"
	"github.com/dapr/kit/logger"
)

var (
	log             = logger.NewLogger("dapr.runtime.wfengine")
	wfBackendLogger = logger.NewLogger("dapr.wfengine.durabletask.backend")
)

// ReservedWorkflowNamePrefix is the prefix daprd uses for managed (in-process)
// workflow names. The Universal API rejects user-supplied names with this prefix
// and the gRPC executor routes names with this prefix to the in-process executor.
const ReservedWorkflowNamePrefix = "dapr.internal."

type Interface interface {
	// Registrar is the consumer-side surface used by the processor to register
	// internal workflows for managed resources (MCPServers, etc.).
	wfregistrar.Registrar

	Run(context.Context) error
	RegisterGrpcServer(*grpc.Server)
	Client() workflows.Workflow
	RuntimeMetadata() *runtimev1pb.MetadataWorkflows
	InProcessExecutor() *inprocess.Executor
	ActivityActorType() string
	WorkflowActorType() string
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
	Security          security.Handler
	InProcessExecutor *inprocess.Executor

	EnableClusteredDeployment       bool
	WorkflowsRemoteActivityReminder bool
	WorkflowHistorySigning          bool

	// MaxRequestBodySize is the gRPC server max message size in bytes. The
	// orchestrator uses it to detect and gracefully stall workflows whose
	// history payload would exceed the GetWorkItems stream limit.
	MaxRequestBodySize int

	// Signer provides cryptographic signing and verification. If nil, history
	// signing is disabled.
	Signer *signer.Signer

	// May be nil when the WorkflowAccessPolicy feature is disabled.
	WorkflowAccessPolicies *workflowacl.Holder
}

type engine struct {
	appID                string
	namespace            string
	actors               actors.Interface
	getWorkItemsCount    atomic.Int32
	mcpRegistrationCount atomic.Int32
	// actorRegLock guards the registration counters and actorsRegistered.
	// Held by the GetWorkItems connect/disconnect callbacks, EnsureActorsRegistered,
	// and UnregisterMCPServer so all paths can read and write the registration
	// state without racing.
	actorRegLock sync.Mutex
	// actorsRegistered tracks whether workflow actor types are currently
	// registered with placement. Guarded by actorRegLock.
	actorsRegistered bool

	worker        backend.TaskHubWorker
	backend       *backendactors.Actors
	client        workflows.Workflow
	inProcessExec *inprocess.Executor
	compStore     *compstore.ComponentStore

	registerGrpcServerFn func(grpcServer grpc.ServiceRegistrar)
}

func New(opts Options) (Interface, error) {
	var retPolicy *config.WorkflowStateRetentionPolicy
	if opts.Spec != nil {
		retPolicy = opts.Spec.StateRetentionPolicy
	}

	// Disable history signing if the WorkflowHistorySigning feature flag is not
	// enabled.
	s := opts.Signer
	if !opts.WorkflowHistorySigning {
		s = nil
	} else if s == nil {
		// The feature flag is explicitly enabled but mTLS is not available. This
		// is a misconfiguration. Signing requires mTLS for the SPIFFE identity
		// used as the signing key.
		return nil, errors.New("WorkflowHistorySigning feature flag is enabled but mTLS is not configured; workflow history signing requires mTLS to be active")
	}

	// If no backend was initialized by the manager, create a backend backed by actors
	abackend := backendactors.New(backendactors.Options{
		AppID:                  opts.AppID,
		Namespace:              opts.Namespace,
		Actors:                 opts.Actors,
		Resiliency:             opts.Resiliency,
		EventSink:              opts.EventSink,
		ComponentStore:         opts.ComponentStore,
		RetentionPolicy:        retPolicy,
		Signer:                 s,
		MaxRequestBodySize:     opts.MaxRequestBodySize,
		WorkflowAccessPolicies: opts.WorkflowAccessPolicies,

		EnableClusteredDeployment:       opts.EnableClusteredDeployment,
		WorkflowsRemoteActivityReminder: opts.WorkflowsRemoteActivityReminder,
	})

	inProcessExec := opts.InProcessExecutor
	if inProcessExec == nil {
		return nil, errors.New("InProcessExecutor is required")
	}

	wfe := &engine{
		appID:         opts.AppID,
		namespace:     opts.Namespace,
		actors:        opts.Actors,
		backend:       abackend,
		inProcessExec: inProcessExec,
		compStore:     opts.ComponentStore,
	}

	grpcExec, registerGrpcServerFn := backend.NewGrpcExecutor(abackend, log,
		backend.WithOnGetWorkItemsConnectionCallback(func(ctx context.Context) error {
			wfe.actorRegLock.Lock()
			defer wfe.actorRegLock.Unlock()

			wfe.getWorkItemsCount.Add(1)
			if !wfe.actorsRegistered {
				log.Debug("Registering workflow actors")
				if err := abackend.RegisterActors(ctx); err != nil {
					wfe.getWorkItemsCount.Add(-1)
					return err
				}
				wfe.actorsRegistered = true
			}

			return nil
		}),
		backend.WithOnGetWorkItemsDisconnectCallback(func(ctx context.Context) error {
			wfe.actorRegLock.Lock()
			defer wfe.actorRegLock.Unlock()

			if ctx.Err() != nil {
				ctx = context.Background()
			}

			if wfe.getWorkItemsCount.Add(-1) == 0 && wfe.mcpRegistrationCount.Load() == 0 && wfe.actorsRegistered {
				log.Debug("Unregistering workflow actors")
				// Reset unconditionally: UnRegisterActors removes types from the
				// table before HaltAll, so an error here still means they're gone.
				err := abackend.UnRegisterActors(ctx)
				wfe.actorsRegistered = false
				if err != nil {
					return err
				}
			}

			return nil
		}),
		backend.WithStreamSendTimeout(time.Second*10),
	)

	var topts []backend.NewTaskWorkerOptions
	if opts.Spec.GetMaxConcurrentWorkflowInvocations() != nil {
		topts = []backend.NewTaskWorkerOptions{
			backend.WithMaxParallelism(*opts.Spec.GetMaxConcurrentWorkflowInvocations()),
		}
	}

	oworker := backend.NewWorkflowWorker(backend.WorkflowWorkerOptions{
		Backend:             abackend,
		Executor:            grpcExec,
		InProcessExecutor:   inProcessExec.Backend(),
		InProcessNamePrefix: ReservedWorkflowNamePrefix,
		Logger:              wfBackendLogger,
		AppID:               opts.AppID,
	}, topts...)

	topts = nil
	if opts.Spec.GetMaxConcurrentActivityInvocations() != nil {
		topts = []backend.NewTaskWorkerOptions{
			backend.WithMaxParallelism(*opts.Spec.GetMaxConcurrentActivityInvocations()),
		}
	}

	aworker := backend.NewActivityTaskWorkerWithInProcess(
		abackend,
		grpcExec,
		inProcessExec.Backend(),
		ReservedWorkflowNamePrefix,
		wfBackendLogger,
		topts...,
	)
	worker := backend.NewTaskHubWorker(abackend, oworker, aworker, wfBackendLogger)

	wfe.worker = worker
	wfe.registerGrpcServerFn = registerGrpcServerFn
	wfe.client = &client{
		logger: wfBackendLogger,
		client: backend.NewTaskHubClient(abackend),
	}
	return wfe, nil
}

// EnsureActorsRegistered bumps the MCP registration count and registers workflow
// actor types with placement if they aren't already. Callers (managed-workflow
// registrars such as MCPServer) MUST pair this with UnregisterMCPServer so the
// count is balanced and actors are torn down when no longer referenced.
func (wfe *engine) EnsureActorsRegistered(ctx context.Context) error {
	wfe.actorRegLock.Lock()
	defer wfe.actorRegLock.Unlock()

	wfe.mcpRegistrationCount.Add(1)
	if !wfe.actorsRegistered {
		log.Debug("Registering workflow actors for internal workflows")
		if err := wfe.backend.RegisterActors(ctx); err != nil {
			wfe.mcpRegistrationCount.Add(-1)
			return err
		}
		wfe.actorsRegistered = true
	}
	return nil
}

// RegisterMCPServer installs workflows and ensures actors are registered.
// Refcount bumps only on success.
func (wfe *engine) RegisterMCPServer(ctx context.Context, server mcpserverapi.MCPServer, store *compstore.ComponentStore, sec security.Handler) error {
	if err := wfe.inProcessExec.RegisterMCPServer(ctx, server, store, sec); err != nil {
		return err
	}
	return wfe.EnsureActorsRegistered(ctx)
}

// UnregisterMCPServer forwards to the in-process executor and decrements the
// MCP registration count. Unregisters workflow actors when the count drops to
// zero AND no external SDK workers are connected.
// Implements processor.internalWorkflowRegistrar.
func (wfe *engine) UnregisterMCPServer(serverName string) {
	wfe.inProcessExec.UnregisterMCPServer(serverName)

	wfe.actorRegLock.Lock()
	defer wfe.actorRegLock.Unlock()

	if wfe.mcpRegistrationCount.Add(-1) == 0 && wfe.getWorkItemsCount.Load() == 0 && wfe.actorsRegistered {
		log.Debug("Unregistering workflow actors")
		err := wfe.backend.UnRegisterActors(context.Background())
		wfe.actorsRegistered = false
		if err != nil {
			log.Warnf("Failed to unregister workflow actors: %s", err)
		}
	}
}

func (wfe *engine) InProcessExecutor() *inprocess.Executor {
	return wfe.inProcessExec
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

func (wfe *engine) WorkflowActorType() string {
	return wfe.backend.WorkflowActorType()
}

func (wfe *engine) RuntimeMetadata() *runtimev1pb.MetadataWorkflows {
	return &runtimev1pb.MetadataWorkflows{
		ConnectedWorkers: wfe.getWorkItemsCount.Load(),
	}
}
