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

	"google.golang.org/protobuf/types/known/anypb"

	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
	"github.com/dapr/durabletask-go/api"
)

func (o *orchestrator) handleStream(ctx context.Context,
	req *internalsv1pb.InternalInvokeRequest,
	stream func(*internalsv1pb.InternalInvokeResponse) (bool, error),
	unlock context.CancelFunc,
) (bool, error) {
	if m := req.GetMessage().GetMethod(); m != todo.WaitForRuntimeStatus {
		return false, fmt.Errorf("unsupported stream method: %s", m)
	}

	_, ometa, err := o.loadInternalState(ctx)
	if err != nil {
		return false, err
	}

	if ometa != nil {
		var arstate *anypb.Any
		arstate, err = anypb.New(ometa)
		if err != nil {
			return false, err
		}

		var ok bool
		ok, err = stream(&internalsv1pb.InternalInvokeResponse{
			Status:  &internalsv1pb.Status{Code: http.StatusOK},
			Message: &commonv1pb.InvokeResponse{Data: arstate},
		})
		if err != nil || ok {
			if api.OrchestrationMetadataIsComplete(ometa) {
				o.factory.deactivate(o)
			}
			return false, err
		}
	}

	idx := o.streamIDx
	o.streamIDx++

	sf := &streamFn{
		fn:    stream,
		errCh: make(chan error, 1),
	}
	defer sf.done.Store(true)

	o.streamFns[idx] = sf

	// unlock this orchestrator actor.
	unlock()

	select {
	case <-ctx.Done():
		return true, ctx.Err()
	case err = <-sf.errCh:
		return true, err
	}
}
