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

package schedule

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/wrapperspb"

	wferrors "github.com/dapr/dapr/pkg/runtime/wfengine/errors"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
)

// listConcurrency bounds the schedules ListWorkflowSchedules reads at once.
const listConcurrency = 16

type createKey struct{}

// CheckCreate rejects a client creating the schedule workflow, or any
// workflow with a schedule's instance ID, other than through the workflow
// schedule API.
func CheckCreate(ctx context.Context, name, instanceID string) error {
	if ctx.Value(createKey{}) != nil {
		return nil
	}
	if name == WorkflowName || strings.HasPrefix(instanceID, InstanceIDPrefix) {
		return status.Errorf(codes.InvalidArgument,
			"the workflow name %q and instance IDs starting with %q are reserved for workflow schedules", WorkflowName, InstanceIDPrefix)
	}
	return nil
}

// Handler serves the workflow schedule calls of the TaskHubSidecarService.
type Handler struct {
	be        backend.Backend
	client    backend.TaskHubClient
	namespace string
	owner     string
}

var _ backend.WorkflowScheduleHandler = (*Handler)(nil)

func NewHandler(be backend.Backend, namespace, appID string) *Handler {
	return &Handler{be: be, client: backend.NewTaskHubClient(be), namespace: namespace, owner: Owner(namespace, appID)}
}

func (h *Handler) CreateWorkflowSchedule(ctx context.Context, req *protos.CreateWorkflowScheduleRequest) (*protos.CreateWorkflowScheduleResponse, error) {
	if req.GetSchedule() == nil {
		return nil, status.Error(codes.InvalidArgument, "a schedule is required")
	}
	ws := proto.Clone(req.GetSchedule()).(*protos.WorkflowSchedule)
	ws.Status = nil
	if err := validate(ws, time.Now()); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	// Workflow calls only reach apps in this namespace.
	if ws.TargetAppNamespace != nil && ws.GetTargetAppNamespace() != h.namespace {
		return nil, status.Errorf(codes.InvalidArgument, "target app namespace %q is not supported: it must be this app's namespace %q", ws.GetTargetAppNamespace(), h.namespace)
	}

	spec, err := protojson.Marshal(ws)
	if err != nil {
		return nil, err
	}
	input, err := json.Marshal(state{Schedule: spec, Owner: h.owner, CreatedAt: time.Now().UTC()})
	if err != nil {
		return nil, err
	}

	id := api.InstanceID(InstanceIDPrefix + ws.GetName())
	md, err := h.client.FetchWorkflowMetadata(ctx, id)
	switch {
	case errors.Is(err, api.ErrInstanceNotFound):
	case err != nil:
		return nil, statusErr(err)
	case md.GetName() != WorkflowName:
		return nil, status.Errorf(codes.AlreadyExists, "instance ID %q is taken by workflow %q", id, md.GetName())
	case !req.GetOverwrite() && md.GetRuntimeStatus() != protos.OrchestrationStatus_ORCHESTRATION_STATUS_TERMINATED:
		return nil, status.Errorf(codes.AlreadyExists, "workflow schedule %q already exists", ws.GetName())
	default:
		if derr := h.delete(ctx, id, md); derr != nil {
			return nil, derr
		}
	}

	if _, err := h.client.ScheduleNewWorkflow(context.WithValue(ctx, createKey{}, true), WorkflowName,
		api.WithInstanceID(id),
		api.WithRawInput(wrapperspb.String(string(input))),
	); err != nil {
		return nil, statusErr(err)
	}

	// Once started, the schedule's status and input are readable.
	if _, err := h.client.WaitForWorkflowStart(ctx, id); err != nil {
		return nil, statusErr(err)
	}
	return new(protos.CreateWorkflowScheduleResponse), nil
}

func (h *Handler) GetWorkflowSchedule(ctx context.Context, req *protos.GetWorkflowScheduleRequest) (*protos.GetWorkflowScheduleResponse, error) {
	ws, err := h.get(ctx, req.GetName())
	if err != nil {
		return nil, err
	}
	return &protos.GetWorkflowScheduleResponse{Schedule: ws}, nil
}

func (h *Handler) ListWorkflowSchedules(ctx context.Context, req *protos.ListWorkflowSchedulesRequest) (*protos.ListWorkflowSchedulesResponse, error) {
	prefix := InstanceIDPrefix
	resp, err := h.be.ListInstanceIDs(ctx, &protos.ListInstanceIDsRequest{
		ContinuationToken: req.ContinuationToken,
		PageSize:          req.PageSize,
		InstanceIdPrefix:  &prefix,
	})
	if err != nil {
		return nil, statusErr(err)
	}

	found := make([]*protos.WorkflowSchedule, len(resp.GetInstanceIds()))
	eg, egctx := errgroup.WithContext(ctx)
	eg.SetLimit(listConcurrency)
	for i, id := range resp.GetInstanceIds() {
		eg.Go(func() error {
			ws, err := h.get(egctx, strings.TrimPrefix(id, InstanceIDPrefix))
			if status.Code(err) == codes.NotFound {
				return nil
			}
			found[i] = ws
			return err
		})
	}
	if err := eg.Wait(); err != nil {
		return nil, err
	}

	schedules := make([]*protos.WorkflowSchedule, 0, len(found))
	for _, ws := range found {
		if ws != nil {
			schedules = append(schedules, ws)
		}
	}
	return &protos.ListWorkflowSchedulesResponse{
		Schedules:         schedules,
		ContinuationToken: resp.ContinuationToken,
	}, nil
}

func (h *Handler) DeleteWorkflowSchedule(ctx context.Context, req *protos.DeleteWorkflowScheduleRequest) (*protos.DeleteWorkflowScheduleResponse, error) {
	md, err := h.fetch(ctx, req.GetName())
	if err != nil {
		return nil, err
	}
	if err := h.delete(ctx, api.InstanceID(InstanceIDPrefix+req.GetName()), md); err != nil {
		return nil, err
	}
	return new(protos.DeleteWorkflowScheduleResponse), nil
}

func (h *Handler) TriggerWorkflowSchedule(ctx context.Context, req *protos.TriggerWorkflowScheduleRequest) (*protos.TriggerWorkflowScheduleResponse, error) {
	md, err := h.fetch(ctx, req.GetName())
	switch {
	case err != nil:
		return nil, err
	case md.GetRuntimeStatus() == protos.OrchestrationStatus_ORCHESTRATION_STATUS_TERMINATED:
		return nil, notFound(req.GetName())
	case api.WorkflowMetadataIsComplete(md):
		return nil, status.Errorf(codes.FailedPrecondition, "workflow schedule %q has no runs left", req.GetName())
	}

	runID := ManualRunInstanceID(req.GetName(), h.owner, time.Now())
	id := api.InstanceID(InstanceIDPrefix + req.GetName())
	if err := h.client.RaiseEvent(ctx, id, triggerEvent, api.WithEventPayload(trigger{InstanceID: runID})); err != nil {
		return nil, statusErr(err)
	}
	return &protos.TriggerWorkflowScheduleResponse{InstanceId: runID}, nil
}

// fetch returns the metadata of a schedule's workflow. An instance with a
// schedule's ID that runs another workflow is not a schedule.
func (h *Handler) fetch(ctx context.Context, name string) (*backend.WorkflowMetadata, error) {
	md, err := h.client.FetchWorkflowMetadata(ctx, api.InstanceID(InstanceIDPrefix+name))
	switch {
	case errors.Is(err, api.ErrInstanceNotFound), err == nil && md.GetName() != WorkflowName:
		return nil, notFound(name)
	case err != nil:
		return nil, statusErr(err)
	}
	return md, nil
}

// get returns a schedule with its status, and how its last run stands.
func (h *Handler) get(ctx context.Context, name string) (*protos.WorkflowSchedule, error) {
	md, err := h.fetch(ctx, name)
	if err != nil {
		return nil, err
	}
	if md.GetRuntimeStatus() == protos.OrchestrationStatus_ORCHESTRATION_STATUS_TERMINATED {
		return nil, notFound(name)
	}

	var s state
	if jerr := json.Unmarshal([]byte(md.GetInput().GetValue()), &s); jerr != nil {
		return nil, status.Errorf(codes.Internal, "workflow schedule %q has an invalid state: %v", name, jerr)
	}
	ws, err := s.schedule()
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	st := s.status()
	if cs := md.GetCustomStatus().GetValue(); cs != "" {
		if err := protojson.Unmarshal([]byte(cs), st); err != nil {
			return nil, status.Errorf(codes.Internal, "workflow schedule %q has an invalid status: %v", name, err)
		}
	}
	if md.GetRuntimeStatus() == protos.OrchestrationStatus_ORCHESTRATION_STATUS_FAILED {
		st.State = protos.WorkflowScheduleState_WORKFLOW_SCHEDULE_STATE_FAILED
		st.NextRunTime = nil
		st.FailureMessage = wrapperspb.String(md.GetFailureDetails().GetErrorMessage())
	}
	if run := st.GetLastRun(); run.GetInstanceId() != "" {
		h.readRun(ctx, ws, run)
	}
	ws.Status = st
	return ws, nil
}

// readRun fills in how a run stands. A run that cannot be read, as one
// purged, keeps only its instance ID.
func (h *Handler) readRun(ctx context.Context, ws *protos.WorkflowSchedule, run *protos.WorkflowScheduleRun) {
	var opts []api.FetchWorkflowMetadataOptions
	if ws.TargetAppID != nil {
		opts = append(opts, api.WithFetchAppID(ws.GetTargetAppID()))
	}
	md, err := h.client.FetchWorkflowMetadata(ctx, api.InstanceID(run.GetInstanceId()), opts...)
	if err != nil {
		return
	}
	rs := md.GetRuntimeStatus()
	run.RuntimeStatus = &rs
	run.CreatedTimestamp = md.GetCreatedAt()
	run.LastUpdatedTimestamp = md.GetLastUpdatedAt()
	run.FailureDetails = md.GetFailureDetails()
	if run.GetFailureDetails() == nil {
		run.Output = md.GetOutput()
	}
}

// delete stops a schedule's workflow if it runs and purges it. The runs it
// started are detached, so they are left alone.
func (h *Handler) delete(ctx context.Context, id api.InstanceID, md *backend.WorkflowMetadata) error {
	if !api.WorkflowMetadataIsComplete(md) {
		if err := h.client.TerminateWorkflow(ctx, id); err != nil {
			return statusErr(err)
		}
		if _, err := h.client.WaitForWorkflowCompletion(ctx, id); err != nil {
			return statusErr(err)
		}
	}
	if err := h.client.PurgeWorkflowState(ctx, id); err != nil && !errors.Is(err, api.ErrInstanceNotFound) {
		return statusErr(err)
	}
	return nil
}

func validate(ws *protos.WorkflowSchedule, now time.Time) error {
	switch {
	case !nameRegexp.MatchString(ws.GetName()):
		return fmt.Errorf("invalid name %q: it must be 1 to 30 letters, digits or '-'", ws.GetName())
	case strings.HasPrefix(ws.GetName(), InstanceIDPrefix):
		return fmt.Errorf("invalid name %q: names starting with %q are reserved", ws.GetName(), InstanceIDPrefix)
	case ws.GetWorkflowName() == "":
		return errors.New("a workflow name is required")
	case ws.GetWorkflowName() == WorkflowName:
		return fmt.Errorf("workflow %q cannot be scheduled", WorkflowName)
	case ws.Repeats != nil && ws.GetRepeats() == 0:
		return errors.New("repeats must be at least 1")
	case ws.GetExpirationTime() != nil && !ws.GetExpirationTime().AsTime().After(now):
		return errors.New("expiration time must be in the future")
	case ws.TargetAppNamespace != nil && ws.TargetAppID == nil:
		return errors.New("a target app namespace requires a target app ID")
	}
	sched, err := parse(ws.GetSchedule())
	if err != nil {
		return err
	}
	if sched.Next(now.UTC()).IsZero() {
		return fmt.Errorf("schedule %q never matches", ws.GetSchedule())
	}
	return nil
}

func notFound(name string) error {
	return status.Errorf(codes.NotFound, "workflow schedule %q not found", name)
}

// statusErr gives a missing workflow worker its own code, which the
// TaskHubSidecarService would otherwise report as Unknown.
func statusErr(err error) error {
	if errors.Is(err, wferrors.ErrNoWorkerConnected) {
		return status.Error(codes.FailedPrecondition, err.Error())
	}
	return err
}
