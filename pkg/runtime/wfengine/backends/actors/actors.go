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

package actors

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/cenkalti/backoff/v4"
	"github.com/google/uuid"

	workflowacl "github.com/dapr/dapr/pkg/acl/workflow"
	"github.com/dapr/dapr/pkg/actors"
	actorsapi "github.com/dapr/dapr/pkg/actors/api"
	actorerrors "github.com/dapr/dapr/pkg/actors/errors"
	"github.com/dapr/dapr/pkg/actors/table"
	targeterrors "github.com/dapr/dapr/pkg/actors/targets/errors"
	"github.com/dapr/dapr/pkg/actors/targets/workflow"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/activity"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/common"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/executor"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/executor/pending"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/orchestrator"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/retentioner"
	"github.com/dapr/dapr/pkg/config"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/messages"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	"github.com/dapr/dapr/pkg/runtime/wfengine/backends/actors/pendingtracker"
	"github.com/dapr/dapr/pkg/runtime/wfengine/state"
	staterrors "github.com/dapr/dapr/pkg/runtime/wfengine/state/errors"
	"github.com/dapr/dapr/pkg/runtime/wfengine/state/list"
	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
	"github.com/dapr/dapr/utils"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/backend/local"
	"github.com/dapr/durabletask-go/backend/runtimestate"
	"github.com/dapr/kit/concurrency"
	"github.com/dapr/kit/crypto/spiffe/signer"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.wfengine.backend.actors")

const (
	WorkflowNameLabelKey    = "workflow"
	ActivityNameLabelKey    = "activity"
	ExecutorNameLabelKey    = "executor"
	RetentionerNameLabelKey = "retentioner"
)

type Options struct {
	AppID          string
	Namespace      string
	Actors         actors.Interface
	Resiliency     resiliency.Provider
	EventSink      orchestrator.EventSink
	ComponentStore *compstore.ComponentStore

	// experimental feature
	// enabling this will use the cluster tasks backend for pending tasks, instead of the default local implementation
	// the cluster tasks backend uses actors to share the state of pending tasks
	// allowing to deploy multiple daprd replicas and expose them through a loadbalancer
	EnableClusteredDeployment bool

	// Enables a feature to make activities send their results to workflow when
	// the workflow is running on a different application. Useful when using
	// cross app workflows. Ensures that activities are not retried forever if
	// the workflow app is not available, and instead queues the result for when
	// the workflow app is back online. Strongly recommended to always be enabled
	// if using the same Dapr version on all daprds.
	WorkflowsRemoteActivityReminder bool
	WorkflowsFastPath               bool

	RetentionPolicy *config.WorkflowStateRetentionPolicy
	Signer          *signer.Signer

	// MaxRequestBodySize is the gRPC server max message size in bytes. The
	// orchestrator uses it to detect and gracefully stall workflows whose
	// history payload would exceed the GetWorkItems stream limit.
	MaxRequestBodySize int

	// May be nil when the WorkflowAccessPolicy feature is disabled.
	WorkflowAccessPolicies *workflowacl.Holder
}

type Actors struct {
	appID                string
	namespace            string
	workflowActorType    string
	activityActorType    string
	retentionerActorType string
	executorActorType    string

	pendingTasksBackend    *pendingtracker.Tracker
	activityExecs          *activityExecutions
	resiliency             resiliency.Provider
	actors                 actors.Interface
	eventSink              orchestrator.EventSink
	compStore              *compstore.ComponentStore
	retentionPolicy        *config.WorkflowStateRetentionPolicy
	signer                 *signer.Signer
	maxRequestBodySize     int
	workflowAccessPolicies *workflowacl.Holder

	enableClusteredDeployment       bool
	workflowsRemoteActivityReminder bool
	workflowsFastPath               bool
	pendingCompletions              *pending.Pending

	orchestrationWorkItemChan chan *backend.WorkflowWorkItem
	activityWorkItemChan      chan *backend.ActivityWorkItem

	// lastEventNano is the highest external-event ingestion timestamp (unix
	// nanoseconds) this backend has issued. It is used to hand out strictly
	// monotonic, process-unique timestamps to external events so that the
	// inbox dedup (dedup.IsDuplicateExternalEvent, keyed on event name and
	// timestamp) can tell two distinct concurrent RaiseEvent calls apart.
	lastEventNano atomic.Int64

	// droppedCompletions counts the completion deliveries swallowed by the
	// test-only DAPR_WORKFLOW_TEST_DROP_ACTIVITY_COMPLETIONS injection.
	droppedCompletions atomic.Int64

	// duplicatedTurnCompletions counts the workflow-turn completions
	// re-delivered by the test-only
	// DAPR_WORKFLOW_TEST_DUPLICATE_TURN_COMPLETIONS injection.
	duplicatedTurnCompletions atomic.Int64

	stopped atomic.Bool
}

var _ backend.Backend = (*Actors)(nil)

func New(opts Options) (*Actors, error) {
	var pendingTasksBackend PendingTasksBackend
	var pendingCompletions *pending.Pending
	if opts.EnableClusteredDeployment {
		pendingCompletions = pending.New()
		var err error
		pendingTasksBackend, err = NewClusterTasksBackend(ClusterTasksBackendOptions{
			Actors:            opts.Actors,
			ExecutorActorType: todo.ActorTypePrefix + opts.Namespace + utils.DotDelimiter + opts.AppID + utils.DotDelimiter + ExecutorNameLabelKey,
			Pending:           pendingCompletions,
		})
		if err != nil {
			return nil, err
		}
	} else {
		pendingTasksBackend = local.NewTasksBackend()
	}

	// Wrapped so pending completions can be cancelled while no executor is
	// connected; see the pendingtracker package.
	trackedPendingTasksBackend := pendingtracker.New(pendingTasksBackend)

	return &Actors{
		appID:                     opts.AppID,
		namespace:                 opts.Namespace,
		workflowActorType:         todo.ActorTypePrefix + opts.Namespace + utils.DotDelimiter + opts.AppID + utils.DotDelimiter + WorkflowNameLabelKey,
		activityActorType:         todo.ActorTypePrefix + opts.Namespace + utils.DotDelimiter + opts.AppID + utils.DotDelimiter + ActivityNameLabelKey,
		executorActorType:         todo.ActorTypePrefix + opts.Namespace + utils.DotDelimiter + opts.AppID + utils.DotDelimiter + ExecutorNameLabelKey,
		retentionerActorType:      todo.ActorTypePrefix + opts.Namespace + utils.DotDelimiter + opts.AppID + utils.DotDelimiter + RetentionerNameLabelKey,
		actors:                    opts.Actors,
		resiliency:                opts.Resiliency,
		pendingTasksBackend:       trackedPendingTasksBackend,
		activityExecs:             newActivityExecutions(),
		compStore:                 opts.ComponentStore,
		orchestrationWorkItemChan: make(chan *backend.WorkflowWorkItem, 1),
		activityWorkItemChan:      make(chan *backend.ActivityWorkItem, 1),
		eventSink:                 opts.EventSink,
		retentionPolicy:           opts.RetentionPolicy,
		signer:                    opts.Signer,
		maxRequestBodySize:        opts.MaxRequestBodySize,
		workflowAccessPolicies:    opts.WorkflowAccessPolicies,

		enableClusteredDeployment:       opts.EnableClusteredDeployment,
		workflowsRemoteActivityReminder: opts.WorkflowsRemoteActivityReminder,
		workflowsFastPath:               opts.WorkflowsFastPath,
		pendingCompletions:              pendingCompletions,
	}, nil
}

func (abe *Actors) RegisterActors(ctx context.Context) error {
	atable, err := abe.actors.Table(ctx)
	if err != nil {
		return err
	}

	actorTypeBuilder := common.NewActorTypeBuilder(abe.namespace)
	oopts := orchestrator.Options{
		AppID:                  abe.appID,
		Namespace:              abe.namespace,
		WorkflowActorType:      abe.workflowActorType,
		ActivityActorType:      abe.activityActorType,
		Resiliency:             abe.resiliency,
		Actors:                 abe.actors,
		RetentionActorType:     abe.retentionerActorType,
		RetentionPolicy:        abe.retentionPolicy,
		Signer:                 abe.signer,
		MaxRequestBodySize:     abe.maxRequestBodySize,
		WorkflowAccessPolicies: abe.workflowAccessPolicies,
		FastPath:               abe.workflowsFastPath,
		Scheduler: func(ctx context.Context, wi *backend.WorkflowWorkItem) error {
			log.Debugf("%s: scheduling workflow execution with durabletask engine", wi.InstanceID)

			select {
			case <-ctx.Done(): // <-- engine is shutting down or a caller timeout expired
				return ctx.Err()
			case abe.orchestrationWorkItemChan <- wi: // blocks until the engine is ready to process the work item
				return nil
			}
		},
		EventSink:        abe.eventSink,
		ActorTypeBuilder: actorTypeBuilder,
	}

	aopts := activity.Options{
		AppID:             abe.appID,
		Namespace:         abe.namespace,
		ActivityActorType: abe.activityActorType,
		WorkflowActorType: abe.workflowActorType,
		Scheduler: func(ctx context.Context, wi *backend.ActivityWorkItem) error {
			log.Debugf(
				"%s: scheduling [%s#%d] activity execution with durabletask engine",
				wi.InstanceID,
				wi.NewEvent.GetTaskScheduled().GetName(),
				wi.NewEvent.GetEventId())

			select {
			case <-ctx.Done(): // engine is shutting down
				return ctx.Err()
			case abe.activityWorkItemChan <- wi: // blocks until the engine is ready to process the work item
				return nil
			}
		},
		Actors:                          abe.actors,
		ActorTypeBuilder:                actorTypeBuilder,
		WorkflowAccessPolicies:          abe.workflowAccessPolicies,
		Signer:                          abe.signer,
		WorkflowsRemoteActivityReminder: abe.workflowsRemoteActivityReminder,
		FastPath:                        abe.workflowsFastPath,
		ExecutionHeld:                   abe.ActivityExecutionHeld,
		RegisterResolver:                abe.RegisterActivityResolver,
	}

	opts := workflow.Options{
		Orchestrator: oopts,
		Activity:     aopts,
		Retentioner: retentioner.Options{
			Actors:            abe.actors,
			WorkflowActorType: abe.workflowActorType,
			ActorType:         abe.retentionerActorType,
		},
		WorkflowActorType:  abe.workflowActorType,
		ActivityActorType:  abe.activityActorType,
		RetentionActorType: abe.retentionerActorType,
		ExecutorActorType:  abe.executorActorType,
	}

	if abe.enableClusteredDeployment {
		opts.Executor = &executor.Options{
			ActorType: abe.executorActorType,
			Actors:    abe.actors,
			Pending:   abe.pendingCompletions,
		}
	}

	factories, err := workflow.Factories(ctx, opts)
	if err != nil {
		return err
	}

	atable.RegisterActorTypes(table.RegisterActorTypeOptions{
		Factories: factories,
	})

	return nil
}

func (abe *Actors) UnRegisterActors(ctx context.Context) error {
	table, err := abe.actors.Table(ctx)
	if err != nil {
		return err
	}

	actorTypes := []string{
		abe.workflowActorType,
		abe.activityActorType,
		abe.retentionerActorType,
	}
	if abe.enableClusteredDeployment {
		actorTypes = append(actorTypes, abe.executorActorType)
	}

	return table.UnRegisterActorTypes(actorTypes...)
}

// requireActorStateStore gates the externally driven workflow operations
// which enter through the actor router. While no actor state store is
// configured the workflow actor types are not advertised to placement, so
// routing would retry indefinitely instead of surfacing an error; fail fast
// with the same error the state-reading paths return. The actor state store
// can be hot reloaded, so this is evaluated per call.
func (abe *Actors) requireActorStateStore() error {
	if _, _, ok := abe.compStore.GetStateStoreActor(); !ok {
		return messages.ErrActorRuntimeNotFound
	}
	return nil
}

// targetWorkflowActorType resolves the workflow actor type for a router
// target, returning the local type when the target is empty or this app. The
// app ID is validated here rather than trusting the caller: requests entering
// through the durabletask gRPC service (rerun, and the client operations SDKs
// can call directly) do not pass through the Universal API validation, and the
// app ID is interpolated into the derived actor type name.
func (abe *Actors) targetWorkflowActorType(target string) (string, error) {
	if target == "" || target == abe.appID {
		return abe.workflowActorType, nil
	}

	if !common.ValidAppID(target) {
		return "", messages.ErrInvalidWorkflowAppID.WithFormat(target)
	}

	return common.NewActorTypeBuilder(abe.namespace).Workflow(target), nil
}

// RerunWorkflowFromEvent implements backend.Backend and reruns a workflow from
// a specific event ID.
func (abe *Actors) RerunWorkflowFromEvent(ctx context.Context, req *backend.RerunWorkflowFromEventRequest) (api.InstanceID, error) {
	if err := abe.requireActorStateStore(); err != nil {
		return "", err
	}

	if len(req.GetSourceInstanceID()) == 0 {
		return "", status.Error(codes.InvalidArgument, "rerun workflow source instance ID is required")
	}

	if req.NewInstanceID == nil {
		u, err := uuid.NewRandom()
		if err != nil {
			return "", fmt.Errorf("failed to generate instance ID: %w", err)
		}

		req.NewInstanceID = new(u.String())
	}

	if req.GetSourceInstanceID() == req.GetNewInstanceID() {
		return "", status.Error(codes.InvalidArgument, "rerun workflow instance ID must be different from the original instance ID")
	}

	// A router targeting another app means the source instance (and therefore
	// the forked instance) lives on that app: fork on its workflow actor. The
	// subsequent RerunWorkflowInstance hop is a same-app self-call there.
	actorType, err := abe.targetWorkflowActorType(req.GetRouter().GetTargetAppID())
	if err != nil {
		return "", err
	}

	requestBytes, err := proto.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("failed to marshal RerunWorkflowFromEvent: %w", err)
	}

	areq := internalsv1pb.NewInternalInvokeRequest(todo.ForkWorkflowHistory).
		WithActor(actorType, req.GetSourceInstanceID()).
		WithData(requestBytes).
		WithContentType(invokev1.ProtobufContentType)

	engine, err := abe.actors.Router(ctx)
	if err != nil {
		return "", err
	}

	_, err = engine.Call(ctx, areq)
	if err != nil {
		return "", err
	}

	return api.InstanceID(req.GetNewInstanceID()), nil
}

// CreateWorkflowInstance implements backend.Backend and creates a new workflow instance.
//
// Internally, creating a workflow instance also creates a new actor with the same ID. The create
// request is saved into the actor's "inbox" and then executed via a reminder thread. If the app is
// scaled out across multiple replicas, the actor might get assigned to a replicas other than this one.
func (abe *Actors) CreateWorkflowInstance(ctx context.Context, req *backend.CreateWorkflowInstanceRequest) error {
	if err := abe.requireActorStateStore(); err != nil {
		return err
	}

	e := req.GetStartEvent()

	var workflowInstanceID string

	if es := e.GetExecutionStarted(); es == nil {
		return errors.New("the history event must be an ExecutionStartedEvent")
	} else if oi := es.GetWorkflowInstance(); oi == nil {
		return errors.New("the ExecutionStartedEvent did not contain orchestration instance information")
	} else {
		workflowInstanceID = oi.GetInstanceId()
	}

	// Stamp the local app as the source before marshalling, mirroring the
	// child-workflow path: the target uses SourceAppID for access policy
	// decisions and for routing completions back to the caller.
	if r := e.GetRouter(); r != nil && r.GetSourceAppID() == "" {
		r.SourceAppID = abe.appID
	}

	// A router targeting another app schedules the workflow on that app's
	// workflow actor rather than the local one.
	actorType, err := abe.targetWorkflowActorType(e.GetRouter().GetTargetAppID())
	if err != nil {
		return err
	}

	// Forward the whole request so the instance ID reuse policy survives the
	// hop to the workflow actor.
	requestBytes, err := proto.Marshal(req)
	if err != nil {
		return fmt.Errorf("failed to marshal CreateWorkflowInstanceRequest: %w", err)
	}

	// Invoke the well-known workflow actor directly, which will be created by
	// this invocation request. Note that this request goes directly to the actor
	// runtime.
	ireq := internalsv1pb.NewInternalInvokeRequest(todo.CreateWorkflowInstanceMethod).
		WithActor(actorType, workflowInstanceID).
		WithData(requestBytes).
		WithContentType(invokev1.ProtobufContentType)
	start := time.Now()

	router, err := abe.actors.Router(ctx)
	if err != nil {
		return err
	}

	err = backoff.Retry(func() error {
		_, eerr := router.Call(ctx, ireq)

		status, ok := status.FromError(eerr)
		if ok && (status.Code() == codes.FailedPrecondition ||
			status.Code() == codes.Unavailable) {
			return eerr
		}

		if errors.Is(eerr, actorerrors.ErrCreatingActor) {
			return eerr
		}

		return backoff.Permanent(eerr)
	}, backoff.WithContext(common.NewJitterBackoff(common.RetryBackoffBase, common.RetryBackoffCap), ctx))

	elapsed := diag.ElapsedSince(start)
	if err != nil {
		// failed request to CREATE workflow, record count and latency metrics.
		diag.DefaultWorkflowMonitoring.WorkflowOperationEvent(ctx, diag.CreateWorkflow, diag.StatusFailed, elapsed)
		return err
	}
	// successful request to CREATE workflow, record count and latency metrics.
	diag.DefaultWorkflowMonitoring.WorkflowOperationEvent(ctx, diag.CreateWorkflow, diag.StatusSuccess, elapsed)

	return nil
}

// GetWorkflowMetadata implements backend.Backend
func (abe *Actors) GetWorkflowMetadata(ctx context.Context, id api.InstanceID, router *protos.TaskRouter) (*backend.WorkflowMetadata, error) {
	if target := router.GetTargetAppID(); target != "" && target != abe.appID {
		return abe.getWorkflowMetadataRemote(ctx, id, target)
	}

	wstate, err := abe.loadInternalState(ctx, id)
	if err != nil {
		return nil, err
	}

	if wstate == nil {
		return nil, api.ErrInstanceNotFound
	}

	rstate := runtimestate.NewWorkflowRuntimeState(string(id), wstate.CustomStatus, wstate.History)

	name, _ := runtimestate.Name(rstate)
	createdAt, _ := runtimestate.CreatedTime(rstate)
	lastUpdated, _ := runtimestate.LastUpdatedTime(rstate)
	input, _ := runtimestate.Input(rstate)
	output, _ := runtimestate.Output(rstate)
	failureDetuils, _ := runtimestate.FailureDetails(rstate)

	var startedAt *timestamppb.Timestamp
	if t := runtimestate.GetStartedTime(rstate); !t.IsZero() {
		startedAt = timestamppb.New(t)
	}

	return &backend.WorkflowMetadata{
		InstanceId:     string(id),
		Name:           name,
		RuntimeStatus:  runtimestate.RuntimeStatus(rstate),
		CreatedAt:      timestamppb.New(createdAt),
		StartedAt:      startedAt,
		LastUpdatedAt:  timestamppb.New(lastUpdated),
		Input:          input,
		Output:         output,
		CustomStatus:   rstate.GetCustomStatus(),
		FailureDetails: failureDetuils,
		Version:        state.WorkflowVersion(rstate.GetOldEvents()),
	}, nil
}

// getWorkflowMetadataRemote fetches metadata for an instance owned by another
// app via a one-shot WaitForRuntimeStatus stream on its workflow actor. The
// MetadataFetchOnly flag makes the target reply immediately (or with
// not-found) instead of parking the stream; an older target daprd ignores the
// flag and this degrades to waiting for the next status change.
func (abe *Actors) getWorkflowMetadataRemote(ctx context.Context, id api.InstanceID, targetAppID string) (*backend.WorkflowMetadata, error) {
	actorType, err := abe.targetWorkflowActorType(targetAppID)
	if err != nil {
		return nil, err
	}

	actorRouter, err := abe.actors.Router(ctx)
	if err != nil {
		return nil, err
	}

	req := internalsv1pb.
		NewInternalInvokeRequest(todo.WaitForRuntimeStatus).
		WithActor(actorType, string(id)).
		WithContentType(invokev1.ProtobufContentType).
		WithMetadata(map[string][]string{
			todo.MetadataFetchOnly: {"true"},
		})

	var meta *backend.WorkflowMetadata
	err = actorRouter.CallStream(ctx, req, func(resp *internalsv1pb.InternalInvokeResponse) (bool, error) {
		// Nonexistence arrives as a not-found status rather than a stream
		// error, which the actor routers would retry as transient.
		if resp.GetStatus().GetCode() == http.StatusNotFound {
			return true, nil
		}
		data := resp.GetMessage().GetData()
		if data == nil {
			return false, fmt.Errorf("workflow metadata response from app '%s' has status %d and no payload", targetAppID, resp.GetStatus().GetCode())
		}
		var m backend.WorkflowMetadata
		if perr := data.UnmarshalTo(&m); perr != nil {
			return false, perr
		}
		meta = &m
		return true, nil
	})
	if err != nil {
		// Actor invocations carry errors as wire strings; normalise not-found
		// so callers can rely on errors.Is.
		if strings.HasSuffix(err.Error(), api.ErrInstanceNotFound.Error()) {
			return nil, api.ErrInstanceNotFound
		}
		return nil, err
	}
	if meta == nil {
		return nil, api.ErrInstanceNotFound
	}
	return meta, nil
}

// AbandonActivityWorkItem implements backend.Backend. It gets called by durabletask-go when there is
// an unexpected failure in the workflow activity execution pipeline.
func (*Actors) AbandonActivityWorkItem(ctx context.Context, wi *backend.ActivityWorkItem) error {
	log.Warnf("%s: aborting activity execution (::%d)", wi.InstanceID, wi.NewEvent.GetEventId())

	// Sending false signals the waiting activity actor to abort the activity execution.
	if channel, ok := wi.Properties[todo.CallbackChannelProperty]; ok {
		channel.(chan bool) <- false
	}

	return nil
}

// AbandonWorkflowWorkItem implements backend.Backend. It gets called by durabletask-go when there is
// an unexpected failure in the workflow orchestration execution pipeline.
func (*Actors) AbandonWorkflowWorkItem(ctx context.Context, wi *backend.WorkflowWorkItem) error {
	log.Warnf("%s: aborting workflow execution", wi.InstanceID)

	// Sending false signals the waiting workflow actor to abort the workflow execution.
	// TODO: @joshvanl: remove
	if channel, ok := wi.Properties[todo.CallbackChannelProperty]; ok {
		channel.(chan bool) <- false
	}

	return nil
}

// AddNewWorkflowEvent implements backend.Backend and sends the event e to the workflow actor identified by id.
func (abe *Actors) AddNewWorkflowEvent(ctx context.Context, id api.InstanceID, e *backend.HistoryEvent) error {
	if err := abe.requireActorStateStore(); err != nil {
		return err
	}

	// External events (RaiseEvent) are stamped with a wall-clock timestamp by
	// the caller and deduped downstream by (event name, timestamp). Two
	// RaiseEvent calls racing on the same wall-clock nanosecond would then be
	// indistinguishable and one would be wrongly dropped as a redelivery. Give
	// each external event a strictly monotonic, process-unique ingestion
	// timestamp here, at the single point where it enters the actor backend, so
	// distinct events never collide while a genuine redelivery (the same
	// already-marshalled bytes resent on actor-call retry) keeps its timestamp.
	if e.GetEventRaised() != nil {
		e.Timestamp = abe.uniqueEventTimestamp()
	}

	// If the event carries a router with a foreign target app ID (e.g. a
	// recursive ExecutionTerminated for a cross-app sub-orchestrator), the
	// event must reach the workflow actor in that other app rather than the
	// local one. Otherwise the local actor reports "no such instance" and
	// retries forever.
	actorType, err := abe.targetWorkflowActorType(e.GetRouter().GetTargetAppID())
	if err != nil {
		return err
	}

	data, err := proto.Marshal(e)
	if err != nil {
		return err
	}

	// Send the event to the corresponding workflow actor, which will store it in its event inbox.
	req := internalsv1pb.
		NewInternalInvokeRequest(todo.AddWorkflowEventMethod).
		WithActor(actorType, string(id)).
		WithData(data).
		WithContentType(invokev1.OctetStreamContentType)

	router, err := abe.actors.Router(ctx)
	if err != nil {
		return err
	}

	start := time.Now()
	_, err = router.Call(ctx, req)

	elapsed := diag.ElapsedSince(start)
	if err != nil {
		// failed request to ADD EVENT, record count and latency metrics.
		diag.DefaultWorkflowMonitoring.WorkflowOperationEvent(ctx, diag.AddEvent, diag.StatusFailed, elapsed)
		return err
	}
	// successful request to ADD EVENT, record count and latency metrics.
	diag.DefaultWorkflowMonitoring.WorkflowOperationEvent(ctx, diag.AddEvent, diag.StatusSuccess, elapsed)

	return nil
}

// uniqueEventTimestamp returns a timestamp that is strictly greater than any
// previously returned by this backend, so that concurrently ingested external
// events are never assigned the same value. It clamps to wall-clock time when
// the clock has advanced and otherwise increments the last issued value by a
// nanosecond, keeping drift negligible under contention.
func (abe *Actors) uniqueEventTimestamp() *timestamppb.Timestamp {
	now := time.Now().UnixNano()
	for {
		prev := abe.lastEventNano.Load()
		next := now
		if next <= prev {
			next = prev + 1
		}
		if abe.lastEventNano.CompareAndSwap(prev, next) {
			return timestamppb.New(time.Unix(0, next))
		}
	}
}

// CompleteActivityWorkItem implements backend.Backend
func (*Actors) CompleteActivityWorkItem(ctx context.Context, wi *backend.ActivityWorkItem) error {
	// Sending true signals the waiting activity actor to complete the execution normally.
	wi.Properties[todo.CallbackChannelProperty].(chan bool) <- true
	return nil
}

// CompleteWorkflowWorkItem implements backend.Backend
func (*Actors) CompleteWorkflowWorkItem(ctx context.Context, wi *backend.WorkflowWorkItem) error {
	// Sending true signals the waiting workflow actor to complete the execution normally.
	wi.Properties[todo.CallbackChannelProperty].(chan bool) <- true
	return nil
}

// CreateTaskHub implements backend.Backend
func (*Actors) CreateTaskHub(context.Context) error {
	return nil
}

// DeleteTaskHub implements backend.Backend
func (*Actors) DeleteTaskHub(context.Context) error {
	return errors.New("not supported")
}

// GetWorkflowRuntimeState implements backend.Backend
func (abe *Actors) GetWorkflowRuntimeState(ctx context.Context, owi *backend.WorkflowWorkItem) (*backend.WorkflowRuntimeState, error) {
	state, err := abe.loadInternalState(ctx, owi.InstanceID)
	if err != nil {
		return nil, err
	}

	if state == nil {
		return nil, api.ErrInstanceNotFound
	}

	runtimeState := runtimestate.NewWorkflowRuntimeState(string(owi.InstanceID), state.CustomStatus, state.History)

	return runtimeState, nil
}

func (abe *Actors) WatchWorkflowRuntimeStatus(ctx context.Context, id api.InstanceID, taskRouter *protos.TaskRouter, condition func(*backend.WorkflowMetadata) bool) error {
	log.Debugf("Actor backend streaming WorkflowRuntimeStatus %s", id)

	// A router targeting another app watches that app's workflow actor. This
	// backs cross-app WaitForWorkflowStart/Completion (e.g. a cross-app
	// schedule returns only once the instance has started on the target app).
	actorType, err := abe.targetWorkflowActorType(taskRouter.GetTargetAppID())
	if err != nil {
		return err
	}

	router, err := abe.actors.Router(ctx)
	if err != nil {
		return err
	}

	req := internalsv1pb.
		NewInternalInvokeRequest(todo.WaitForRuntimeStatus).
		WithActor(actorType, string(id)).
		WithContentType(invokev1.ProtobufContentType)

	wait := time.Millisecond * 500
	for {
		err = router.CallStream(ctx, req, func(resp *internalsv1pb.InternalInvokeResponse) (bool, error) {
			var meta backend.WorkflowMetadata
			perr := resp.GetMessage().GetData().UnmarshalTo(&meta)
			if perr != nil {
				log.Errorf("Failed to unmarshal orchestration metadata: %s", perr)
				return false, perr
			}

			return condition(&meta), nil
		})
		switch {
		case err == nil:
			return nil
		// Actor invocations carry errors as wire strings; normalise not-found
		// so callers can rely on errors.Is.
		case strings.HasSuffix(err.Error(), api.ErrInstanceNotFound.Error()):
			return api.ErrInstanceNotFound
		case !targeterrors.IsStalled(err):
			return err
		}

		// A stalled actor rejects stream registrations while the stall turn
		// parks holding the turn lock, but the instance is quiescent and its
		// status readable from the store. A condition the instance already
		// reached must resolve (a schedule's wait-for-start racing a fast
		// stall); otherwise re-register with backoff until the stall clears.
		if meta, merr := abe.GetWorkflowMetadata(ctx, id, taskRouter); merr == nil && condition(meta) {
			return nil
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		wait = min(wait*2, time.Second*5)
	}
}

// PurgeWorkflowState implements backend.Backend.
//
// When router is nil or targets the local app, this is a single-instance
// purge of id (recursive is ignored, the driver walks children itself) and
// returns 1 on success.
//
// When router carries a foreign TargetAppID the instance lives on that app,
// so we delegate via an actor invocation: recursively when recursive is true
// (always the case for cross-app children reached by the recursive purge
// driver), the remote daprd's workflow actor handles its own subtree and
// returns the count. Mirrors the "each app handles its own subtree" model
// that recursive terminate already uses.
//
// The recursive flag is only ever set together with a foreign router (the
// driver walks local descendants itself), so the remote delegation path
// above already covers it and it needs no separate handling here.
func (abe *Actors) PurgeWorkflowState(ctx context.Context, id api.InstanceID, router *protos.TaskRouter, recursive bool, force bool) (int, error) {
	if err := abe.requireActorStateStore(); err != nil {
		return 0, err
	}

	start := time.Now()

	count, err := abe.purgeWorkflowState(ctx, id, router, recursive, force)

	elapsed := diag.ElapsedSince(start)
	if err != nil {
		diag.DefaultWorkflowMonitoring.WorkflowOperationEvent(ctx, diag.PurgeWorkflow, diag.StatusFailed, elapsed)
		return 0, err
	}

	diag.DefaultWorkflowMonitoring.WorkflowOperationEvent(ctx, diag.PurgeWorkflow, diag.StatusSuccess, elapsed)
	return count, nil
}

func (abe *Actors) purgeWorkflowState(ctx context.Context, id api.InstanceID, router *protos.TaskRouter, recursive bool, force bool) (int, error) {
	if target := router.GetTargetAppID(); target != "" && target != abe.appID {
		return abe.purgeWorkflowRemote(ctx, id, target, recursive, force)
	}

	if force {
		if err := abe.purgeWorkflowForce(ctx, id); err != nil {
			return 0, err
		}
	} else {
		if err := abe.purgeWorkflow(ctx, id); err != nil {
			return 0, err
		}
	}

	return 1, nil
}

// purgeWorkflowRemote dispatches a purge actor invocation to the workflow
// actor on the target app and decodes the count of instances purged.
func (abe *Actors) purgeWorkflowRemote(ctx context.Context, id api.InstanceID, targetAppID string, recursive bool, force bool) (int, error) {
	actorType, err := abe.targetWorkflowActorType(targetAppID)
	if err != nil {
		return 0, err
	}

	actorRouter, err := abe.actors.Router(ctx)
	if err != nil {
		return 0, err
	}

	if !recursive {
		// Force purge is a backend-local state store operation that the plain
		// purge actor method does not implement.
		if force {
			return 0, errors.New("cross-app purge does not support force without recursive")
		}
		req := internalsv1pb.
			NewInternalInvokeRequest(todo.PurgeWorkflowStateMethod).
			WithActor(actorType, string(id))
		if _, err = actorRouter.Call(ctx, req); err != nil {
			if strings.HasSuffix(err.Error(), api.ErrInstanceNotFound.Error()) {
				return 0, api.ErrInstanceNotFound
			}
			return 0, err
		}
		return 1, nil
	}

	req := internalsv1pb.
		NewInternalInvokeRequest(todo.RecursivePurgeWorkflowStateMethod).
		WithActor(actorType, string(id))

	if force {
		req = req.WithMetadata(map[string][]string{
			todo.MetadataPurgeForce: {"true"},
		})
	}

	resp, err := actorRouter.Call(ctx, req)
	if err != nil {
		// Actor invocations carry errors as wire strings, so api.ErrInstanceNotFound
		// from the remote handler arrives unwrapped. Normalise so callers
		// (durabletask-go's recursive driver) can rely on errors.Is.
		if strings.HasSuffix(err.Error(), api.ErrInstanceNotFound.Error()) {
			return 0, api.ErrInstanceNotFound
		}
		return 0, err
	}

	respProto := new(protos.PurgeInstancesResponse)
	if err := proto.Unmarshal(resp.GetMessage().GetData().GetValue(), respProto); err != nil {
		return 0, fmt.Errorf("failed to decode recursive purge response: %w", err)
	}
	return int(respProto.GetDeletedInstanceCount()), nil
}

// Start implements backend.Backend
func (abe *Actors) Start(ctx context.Context) error {
	abe.stopped.Store(false)
	return nil
}

// Stop implements backend.Backend
func (abe *Actors) Stop(context.Context) error {
	abe.stopped.Store(true)
	return nil
}

// String displays the type information
func (abe *Actors) String() string {
	return "dapr.actors/v1"
}

func (abe *Actors) loadInternalState(ctx context.Context, id api.InstanceID) (*state.State, error) {
	astate, err := abe.actors.State(ctx)
	if err != nil {
		return nil, err
	}
	if astate == nil {
		return nil, messages.ErrActorRuntimeNotFound
	}

	// actor id is workflow instance id. Tamper recovery (appending the
	// terminal failed event) is the orchestrator actor's responsibility, not
	// the read path's — readers surface the verification error to clients
	// and let them detect it.
	wstate, err := state.LoadWorkflowState(ctx, astate, string(id), state.Options{
		AppID:             abe.appID,
		Namespace:         abe.namespace,
		WorkflowActorType: abe.workflowActorType,
		ActivityActorType: abe.activityActorType,
		Signer:            abe.signer,
	})
	if err != nil {
		return nil, err
	}

	if wstate == nil {
		// No such state exists in the state store
		return nil, nil
	}

	return wstate, nil
}

// NextWorkflowWorkItem implements backend.Backend
func (abe *Actors) NextWorkflowWorkItem(ctx context.Context) (*backend.WorkflowWorkItem, error) {
	// Wait for the workflow actor to signal us with some work to do
	select {
	case wi := <-abe.orchestrationWorkItemChan:
		log.Debugf("Actor backend received a workflow task for workflow '%s'.", wi.InstanceID)
		return wi, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// NextActivityWorkItem implements backend.Backend
func (abe *Actors) NextActivityWorkItem(ctx context.Context) (*backend.ActivityWorkItem, error) {
	// Wait for the activity actor to signal us with some work to do
	select {
	case wi := <-abe.activityWorkItemChan:
		log.Debugf(
			"Actor backend received a [%s#%d] activity task for workflow '%s'.",
			wi.NewEvent.GetTaskScheduled().GetName(),
			wi.NewEvent.GetEventId(),
			wi.InstanceID)

		return wi, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (abe *Actors) ActivityActorType() string {
	return abe.activityActorType
}

func (abe *Actors) WorkflowActorType() string {
	return abe.workflowActorType
}

func (abe *Actors) SetExecutorAvailable(available bool) {
	abe.pendingTasksBackend.SetExecutorAvailable(available)
}

// CancelActivityTask implements backend.Backend.
func (abe *Actors) CancelActivityTask(ctx context.Context, instanceID api.InstanceID, taskID int32) error {
	return abe.callWithBackoff(ctx, func() error {
		return abe.pendingTasksBackend.CancelActivityTask(ctx, instanceID, taskID)
	})
}

// CancelWorkflowTask implements backend.Backend.
func (abe *Actors) CancelWorkflowTask(ctx context.Context, instanceID api.InstanceID) error {
	return abe.callWithBackoff(ctx, func() error {
		return abe.pendingTasksBackend.CancelWorkflowTask(ctx, instanceID)
	})
}

// CompleteActivityTask implements backend.Backend.
func (abe *Actors) CompleteActivityTask(ctx context.Context, response *protos.ActivityResponse) error {
	return abe.callWithBackoff(ctx, func() error {
		return abe.pendingTasksBackend.CompleteActivityTask(ctx, response)
	})
}

// CompleteWorkflowTask implements backend.Backend.
func (abe *Actors) CompleteWorkflowTask(ctx context.Context, response *protos.WorkflowResponse) error {
	err := abe.callWithBackoff(ctx, func() error {
		return abe.pendingTasksBackend.CompleteWorkflowTask(ctx, response)
	})
	if err == nil {
		abe.maybeDuplicateTurnCompletionForTest(response)
	}
	return err
}

func (abe *Actors) callWithBackoff(ctx context.Context, fn func() error) error {
	return backoff.Retry(func() error {
		err := fn()

		switch {
		case err == nil:
			return nil

		case api.IsUnknownTaskIDError(err), api.IsUnknownInstanceIDError(err):
			log.Warnf("Ignoring complete task which no longer exists: %s", err)
			return nil

		case abe.stopped.Load():
			return backoff.Permanent(err)

		case ctx.Err() == nil:
			log.Warnf("error completing activity task: %v, retrying...", err)
		}

		return err
	}, backoff.WithContext(
		backoff.NewExponentialBackOff(
			backoff.WithMaxInterval(3*time.Second),
			backoff.WithRandomizationFactor(0.3),
		), ctx))
}

// OnActivityCompletion implements backend.CompletionCallbackBackend, flipping
// the durabletask worker onto its event-driven completion path: the app
// roundtrip no longer parks a waiter goroutine per in-flight work item. The
// registration is mirrored into activityExecs so the activity target's
// stale-claim eviction can tell a live execution from a lost work item. The
// callback contract keeps registrations armed across deliveries (a stale
// completion token is discarded downstream and the arbiter keeps waiting),
// so held must NOT be released per delivery: it is released only by the
// returned closure, which durabletask invokes exactly once at settlement
// (accepted delivery or abandonment), keeping held-liveness congruent with
// the armed registration.
func (abe *Actors) OnActivityCompletion(request *protos.ActivityRequest, cb func(*protos.ActivityResponse, error)) func() {
	key := activityExecutionKey(request.GetWorkflowInstance().GetInstanceId(), request.GetTaskId())
	release := abe.activityExecs.add(key)
	dereg := abe.pendingTasksBackend.OnActivityCompletion(request, func(resp *protos.ActivityResponse, err error) {
		if abe.dropActivityCompletionForTest() {
			// The injection models the engine losing the work item (host
			// death, registration gone), so the held mirror must drop with
			// it: stale-claim eviction is exactly the rescue under test.
			release()
			log.Warnf("TEST INJECTION: dropping activity completion delivery for instance '%s' task %d", request.GetWorkflowInstance().GetInstanceId(), request.GetTaskId())
			return
		}
		if err == nil {
			// Handshake with the owner execution before this delivery can
			// settle the registration (settlement releases held): mark the
			// owner call resolving so the gap between release and the
			// owner's own callback hop cannot be misread as a stale claim.
			// A discarded stale delivery resolves early, which is benign:
			// held stays true while the registration remains armed. Error
			// deliveries must not resolve: no owner callback follows, and
			// a resolving mark would suppress the eviction that unsticks a
			// genuinely lost execution.
			abe.activityExecs.resolve(key)
		}
		cb(resp, err)
	})
	return func() {
		release()
		dereg()
	}
}

// ActivityExecutionHeld reports whether the engine on this host currently
// holds a completion registration for the given activity work item; wired
// into the activity target as its stale-claim liveness oracle.
// RegisterActivityResolver wires the owner execution's resolve hook into
// the completion waiter's pre-release handshake; see registerResolver.
func (abe *Actors) RegisterActivityResolver(instanceID string, taskID int32, resolve func()) func() {
	return abe.activityExecs.registerResolver(instanceID, taskID, resolve)
}

func (abe *Actors) ActivityExecutionHeld(instanceID string, taskID int32) bool {
	return abe.activityExecs.heldFor(instanceID, taskID)
}

func (abe *Actors) dropActivityCompletionForTest() bool {
	budget := testDropActivityCompletions()
	if budget == 0 {
		return false
	}
	return abe.droppedCompletions.Add(1) <= budget
}

// maybeDuplicateTurnCompletionForTest re-delivers the given workflow-turn
// completion once, after a short delay, when the test-only
// DAPR_WORKFLOW_TEST_DUPLICATE_TURN_COMPLETIONS budget allows. See
// testDuplicateTurnCompletions.
func (abe *Actors) maybeDuplicateTurnCompletionForTest(response *protos.WorkflowResponse) {
	budget := testDuplicateTurnCompletions()
	if budget == 0 || abe.duplicatedTurnCompletions.Add(1) > budget {
		return
	}
	dup := proto.Clone(response).(*protos.WorkflowResponse)
	log.Warnf("TEST INJECTION: re-delivering workflow turn completion for instance '%s'", dup.GetInstanceId())
	go func() {
		time.Sleep(150 * time.Millisecond)
		if err := abe.pendingTasksBackend.CompleteWorkflowTask(context.Background(), dup); err != nil {
			log.Warnf("TEST INJECTION: duplicate turn completion delivery failed: %v", err)
		}
	}()
}

// OnWorkflowTaskCompletion implements backend.CompletionCallbackBackend.
func (abe *Actors) OnWorkflowTaskCompletion(request *protos.WorkflowRequest, cb func(*protos.WorkflowResponse, error)) func() {
	return abe.pendingTasksBackend.OnWorkflowTaskCompletion(request, cb)
}

func (abe *Actors) ListInstanceIDs(ctx context.Context, req *protos.ListInstanceIDsRequest) (*protos.ListInstanceIDsResponse, error) {
	resp, err := list.ListInstanceIDs(ctx, list.ListOptions{
		ComponentStore:    abe.compStore,
		Namespace:         abe.namespace,
		AppID:             abe.appID,
		PageSize:          req.PageSize,
		ContinuationToken: req.ContinuationToken,
	})
	if err != nil {
		return nil, err
	}

	return &protos.ListInstanceIDsResponse{
		InstanceIds:       resp.Keys,
		ContinuationToken: resp.ContinuationToken,
	}, nil
}

func (abe *Actors) GetInstanceHistory(ctx context.Context, req *protos.GetInstanceHistoryRequest) (*protos.GetInstanceHistoryResponse, error) {
	ss, err := abe.actors.State(ctx)
	if err != nil {
		return nil, err
	}
	if ss == nil {
		return nil, messages.ErrActorRuntimeNotFound
	}

	resp, err := state.LoadWorkflowState(ctx, ss, req.GetInstanceId(), state.Options{
		AppID:             abe.appID,
		Namespace:         abe.namespace,
		WorkflowActorType: abe.workflowActorType,
		ActivityActorType: abe.activityActorType,
		Signer:            abe.signer,
	})
	if err != nil {
		return nil, err
	}

	if resp == nil {
		return nil, status.Errorf(codes.NotFound, "workflow instance '%s' not found", req.GetInstanceId())
	}

	return &protos.GetInstanceHistoryResponse{Events: resp.History}, nil
}

func (abe *Actors) purgeWorkflow(ctx context.Context, id api.InstanceID) error {
	req := internalsv1pb.
		NewInternalInvokeRequest(todo.PurgeWorkflowStateMethod).
		WithActor(abe.workflowActorType, string(id))

	router, err := abe.actors.Router(ctx)
	if err != nil {
		return err
	}

	_, err = router.Call(ctx, req)
	if err != nil {
		return err
	}

	return nil
}

func (abe *Actors) purgeWorkflowForce(ctx context.Context, id api.InstanceID) error {
	log.Warnf("Force purging workflow state of '%s'. This can cause corruption if the workflow is being processed", id.String())

	astate, err := abe.actors.State(ctx)
	if err != nil {
		return err
	}
	if astate == nil {
		return messages.ErrActorRuntimeNotFound
	}

	s, err := state.LoadWorkflowState(ctx, astate, id.String(), state.Options{
		AppID:             abe.appID,
		Namespace:         abe.namespace,
		WorkflowActorType: abe.workflowActorType,
		ActivityActorType: abe.activityActorType,
		Signer:            abe.signer,
	})
	if err != nil {
		// Force purge is the escape hatch for instances that cannot be
		// handled normally, which includes tampered or misconfigured signed
		// state: purge the loaded rows anyway rather than refusing.
		var verifyErr *staterrors.VerificationError
		var configErr *staterrors.ConfigurationError
		if s == nil || (!errors.As(err, &verifyErr) && !errors.As(err, &configErr)) {
			return err
		}
		log.Warnf("Force purging workflow '%s' whose state failed signature verification or signing configuration checks: %v", id.String(), err)
	}

	req, err := s.GetPurgeRequest(id.String())
	if err != nil {
		return err
	}

	reminders, err := abe.actors.Reminders(ctx)
	if err != nil {
		return err
	}

	sched, err := reminders.Scheduler()
	if err != nil {
		return err
	}

	return concurrency.Join(ctx,
		func(ctx context.Context) error {
			return astate.TransactionalStateOperation(ctx, true, req, false)
		},
		func(ctx context.Context) error {
			return sched.DeleteByActorID(ctx, &actorsapi.DeleteRemindersByActorIDRequest{
				ActorType:       abe.workflowActorType,
				ActorID:         id.String(),
				MatchIDAsPrefix: false,
			})
		},
		func(ctx context.Context) error {
			return sched.DeleteByActorID(ctx, &actorsapi.DeleteRemindersByActorIDRequest{
				ActorType:       abe.activityActorType,
				ActorID:         id.String() + "::",
				MatchIDAsPrefix: true,
			})
		},
		func(ctx context.Context) error {
			return sched.DeleteByActorID(ctx, &actorsapi.DeleteRemindersByActorIDRequest{
				ActorType:       abe.retentionerActorType,
				ActorID:         id.String(),
				MatchIDAsPrefix: false,
			})
		},
	)
}
