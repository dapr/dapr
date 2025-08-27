/*
Copyright 2025 The Dapr Authors
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

package orchestrator

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"google.golang.org/protobuf/types/known/anypb"

	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	wfenginestate "github.com/dapr/dapr/pkg/runtime/wfengine/state"
	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/backend/runtimestate"
)

func (o *orchestrator) handleStream(ctx context.Context, req *internalsv1pb.InternalInvokeRequest, stream chan<- *internalsv1pb.InternalInvokeResponse) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	ch, err := o.handleStreamInitial(ctx, req, stream)
	if err != nil {
		return err
	}

	ticker := time.NewTicker(time.Second * 4)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err = o.sendCurrentState(ctx, ch, stream); err != nil {
				return err
			}

		case val, ok := <-ch:
			if !ok {
				return o.sendCurrentState(ctx, ch, stream)
			}

			if err = o.sendStateToStream(ctx, ch, stream, val); err != nil {
				return fmt.Errorf("failed to send state to stream: %w", err)
			}
		}
	}
}

func (o *orchestrator) sendCurrentState(ctx context.Context, ch chan *backend.OrchestrationMetadata, stream chan<- *internalsv1pb.InternalInvokeResponse) error {
	unlock, err := o.lock.ContextLock(ctx)
	if err != nil {
		return err
	}
	defer unlock()

	state, err := wfenginestate.LoadWorkflowState(ctx, o.actorState, o.actorID, wfenginestate.Options{
		AppID:             o.appID,
		WorkflowActorType: o.actorType,
		ActivityActorType: o.activityActorType,
	})
	if err != nil {
		return err
	}
	if state == nil {
		return nil
	}

	ometa := o.ometaFromState(
		runtimestate.NewOrchestrationRuntimeState(o.actorID, state.CustomStatus, state.History),
		o.getExecutionStartedEvent(state),
	)

	if err = o.sendStateToStream(ctx, ch, stream, ometa); err != nil {
		return fmt.Errorf("failed to send state to stream: %w", err)
	}

	return nil
}

func (o *orchestrator) sendStateToStream(ctx context.Context,
	ch chan *backend.OrchestrationMetadata,
	stream chan<- *internalsv1pb.InternalInvokeResponse,
	val *backend.OrchestrationMetadata,
) error {
	d, err := anypb.New(val)
	if err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case stream <- &internalsv1pb.InternalInvokeResponse{
		Status:  &internalsv1pb.Status{Code: http.StatusOK},
		Message: &commonv1pb.InvokeResponse{Data: d},
	}:
		return nil
	}
}

func (o *orchestrator) handleStreamInitial(ctx context.Context, req *internalsv1pb.InternalInvokeRequest, stream chan<- *internalsv1pb.InternalInvokeResponse) (chan *backend.OrchestrationMetadata, error) {
	if m := req.GetMessage().GetMethod(); m != todo.WaitForRuntimeStatus {
		return nil, fmt.Errorf("unsupported stream method: %s", m)
	}

	unlock, err := o.lock.ContextLock(ctx)
	if err != nil {
		return nil, err
	}

	ch := make(chan *backend.OrchestrationMetadata)
	o.ometaBroadcaster.Subscribe(ctx, ch)

	_, ometa, err := o.loadInternalState(ctx)
	unlock()
	if err != nil {
		return nil, err
	}

	if ometa == nil {
		return ch, nil
	}

	arstate, err := anypb.New(ometa)
	if err != nil {
		return nil, err
	}

	if api.OrchestrationMetadataIsComplete(ometa) {
		o.factory.deactivate(o)
	}

	select {
	case <-ctx.Done():
	case stream <- &internalsv1pb.InternalInvokeResponse{
		Status:  &internalsv1pb.Status{Code: http.StatusOK},
		Message: &commonv1pb.InvokeResponse{Data: arstate},
	}:
	}

	return ch, nil
}
