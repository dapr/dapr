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
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	ch, err := o.handleStreamInitial(ctx, req, stream)
	if err != nil {
		return err
	}

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

func (o *orchestrator) handleStreamInitial(ctx context.Context, req *internalsv1pb.InternalInvokeRequest, stream chan<- *internalsv1pb.InternalInvokeResponse) (chan *backend.OrchestrationMetadata, error) {
	if m := req.GetMessage().GetMethod(); m != todo.WaitForRuntimeStatus {
		return nil, fmt.Errorf("unsupported stream method: %s", m)
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case o.lock <- struct{}{}:
	}

	ch := make(chan *backend.OrchestrationMetadata)
	o.ometaBroadcaster.Subscribe(ctx, ch)

	_, ometa, err := o.loadInternalState(ctx)
	if err != nil {
		return nil, err
	}

	<-o.lock

	if ometa != nil {
		arstate, err := anypb.New(ometa)
		if err != nil {
			return nil, err
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

	return ch, nil
}
