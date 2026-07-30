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

package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	wfenginestate "github.com/dapr/dapr/pkg/runtime/wfengine/state"
	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/backend"
)

const childTerminalCheckTimeout = 5 * time.Second

// childrenTerminalCheck verifies every child recorded in history is in a
// terminal state, tolerating children that were purged out-of-band.
func (o *orchestrator) childrenTerminalCheck(ctx context.Context, state *wfenginestate.State) error {
	for _, child := range collectChildren(state.History) {
		actorType := o.actorType
		if child.targetAppID != "" && child.targetAppID != o.appID {
			actorType = o.actorTypeBuilder.Workflow(child.targetAppID)
		}

		err := o.childTerminalCheck(ctx, actorType, child.instanceID)
		if err == nil {
			continue
		}

		switch {
		case errors.Is(err, api.ErrInstanceNotFound):
			continue
		case strings.HasSuffix(err.Error(), api.ErrNotCompleted.Error()):
			// Wrap rather than replace so a deep descendant's report keeps the
			// full path, and the message keeps ending in api.ErrNotCompleted
			// for suffix matching at the next level up.
			return fmt.Errorf("child workflow '%s': %w", child.instanceID, err)
		default:
			return fmt.Errorf("failed to verify child workflow '%s' is in a terminal state: %w", child.instanceID, err)
		}
	}

	return nil
}

// childTerminalCheck reads the child's verdict from the first
// WaitForRuntimeStatus stream response, ending the stream immediately rather
// than waiting for a status change. The verdict rides in the response status
// code rather than a handler error, since CallStream retries handler errors
// via the built-in resiliency policy: 404 means the child was purged, 409
// carries a non-terminal-subtree report, and anything else is the regular
// metadata reply (the only reply an older daprd sends).
func (o *orchestrator) childTerminalCheck(ctx context.Context, actorType, instanceID string) error {
	ctx, cancel := context.WithTimeout(ctx, childTerminalCheckTimeout)
	defer cancel()

	req := internalsv1pb.
		NewInternalInvokeRequest(todo.WaitForRuntimeStatus).
		WithActor(actorType, instanceID).
		WithContentType(invokev1.ProtobufContentType).
		WithMetadata(map[string][]string{
			todo.MetadataCheckSubtreeTerminal: {"true"},
		})

	var checkErr error
	var complete bool
	err := o.router.CallStream(ctx, req, func(resp *internalsv1pb.InternalInvokeResponse) (bool, error) {
		switch resp.GetStatus().GetCode() {
		case http.StatusNotFound:
			checkErr = api.ErrInstanceNotFound

		case http.StatusConflict:
			var report wrapperspb.StringValue
			if uerr := resp.GetMessage().GetData().UnmarshalTo(&report); uerr == nil && report.GetValue() != "" {
				checkErr = errors.New(report.GetValue())
			} else {
				checkErr = api.ErrNotCompleted
			}

		default:
			var meta backend.WorkflowMetadata
			if uerr := resp.GetMessage().GetData().UnmarshalTo(&meta); uerr != nil {
				return false, uerr
			}
			complete = api.WorkflowMetadataIsComplete(&meta)
		}
		return true, nil
	})
	if err != nil {
		return err
	}
	if checkErr != nil {
		return checkErr
	}
	if !complete {
		return api.ErrNotCompleted
	}
	return nil
}

// subtreeTerminalResponse resolves a WaitForRuntimeStatus call carrying the
// subtree-terminal metadata flag.
func (o *orchestrator) subtreeTerminalResponse(ctx context.Context, state *wfenginestate.State, ometa *backend.WorkflowMetadata) (*internalsv1pb.InternalInvokeResponse, error) {
	if ometa == nil {
		// Purged or never existed: nothing left to leak into a recreated ancestor.
		return &internalsv1pb.InternalInvokeResponse{
			Status: &internalsv1pb.Status{Code: http.StatusNotFound},
		}, nil
	}

	if !api.WorkflowMetadataIsComplete(ometa) {
		return nil, nil
	}

	cerr := o.childrenTerminalCheck(ctx, state)
	if cerr == nil {
		return nil, nil
	}

	report, err := anypb.New(wrapperspb.String(cerr.Error()))
	if err != nil {
		return nil, err
	}
	return &internalsv1pb.InternalInvokeResponse{
		Status:  &internalsv1pb.Status{Code: http.StatusConflict},
		Message: &commonv1pb.InvokeResponse{Data: report},
	}, nil
}
