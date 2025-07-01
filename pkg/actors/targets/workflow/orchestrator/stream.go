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
	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/backend"
)

func (o *orchestrator) handleStream(ctx context.Context, req *internalsv1pb.InternalInvokeRequest, stream chan<- *internalsv1pb.InternalInvokeResponse) error {
	if err := o.handleStreamInitial(ctx, req, stream); err != nil {
		return err
	}

	subCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	ch := make(chan *backend.OrchestrationMetadata)
	o.ometaBroadcaster.Subscribe(subCtx, ch)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-o.closeCh:
			return nil
		case val, ok := <-ch:
			if !ok {
				return nil
			}
			d, err := anypb.New(val)
			if err != nil {
				return err
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-o.closeCh:
				return nil
			case stream <- &internalsv1pb.InternalInvokeResponse{
				Status:  &internalsv1pb.Status{Code: http.StatusOK},
				Message: &commonv1pb.InvokeResponse{Data: d},
			}:
			}
		}
	}
}

func (o *orchestrator) handleStreamInitial(ctx context.Context, req *internalsv1pb.InternalInvokeRequest, stream chan<- *internalsv1pb.InternalInvokeResponse) error {
	if m := req.GetMessage().GetMethod(); m != todo.WaitForRuntimeStatus {
		return fmt.Errorf("unsupported stream method: %s", m)
	}

	_, ometa, err := o.loadInternalState(ctx)
	if err != nil {
		return err
	}

	if ometa != nil {
		arstate, err := anypb.New(ometa)
		if err != nil {
			return err
		}

		select {
		case <-ctx.Done():
		case stream <- &internalsv1pb.InternalInvokeResponse{
			Status:  &internalsv1pb.Status{Code: http.StatusOK},
			Message: &commonv1pb.InvokeResponse{Data: arstate},
		}:
		}

		if api.OrchestrationMetadataIsComplete(ometa) {
			o.table.DeleteFromTableIn(o, time.Second*10)
		}
	}

	return nil
}
