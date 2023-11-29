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

package universalapi

import (
	"context"
	"fmt"
	"time"

	"github.com/dapr/dapr/pkg/actors"
	"github.com/dapr/dapr/pkg/messages"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (a *UniversalAPI) SetActorRuntime(actor actors.ActorRuntime) {
	a.Actors = actor
}

// SetActorsInitDone indicates that the actors runtime has been initialized, whether actors are available or not
func (a *UniversalAPI) SetActorsInitDone() {
	if a.actorsReady.CompareAndSwap(false, true) {
		close(a.actorsReadyCh)
	}
}

// WaitForActorsReady blocks until the actor runtime is set in the object (or until the context is canceled).
func (a *UniversalAPI) WaitForActorsReady(ctx context.Context) {
	// Quick check to avoid allocating a timer if the actors are ready
	if a.actorsReady.Load() {
		return
	}

	waitCtx, waitCancel := context.WithTimeout(ctx, 10*time.Second)
	defer waitCancel()

	// In both cases, it's a no-op as we check for the actors runtime to be ready below
	select {
	case <-waitCtx.Done():
	case <-a.actorsReadyCh:
	}
}

// This function makes sure that the actor subsystem is ready.
func (a *UniversalAPI) ActorReadinessCheck(ctx context.Context) error {
	a.WaitForActorsReady(ctx)

	if a.Actors == nil {
		a.Logger.Debug(messages.ErrActorRuntimeNotFound)
		return messages.ErrActorRuntimeNotFound
	}

	return nil
}

func (a *UniversalAPI) DeleteActorState(ctx context.Context, in *runtimev1pb.DeleteActorStateRequest) (*runtimev1pb.DeleteActorStateResponse, error) {
	if err := a.ActorReadinessCheck(ctx); err != nil {
		return &runtimev1pb.DeleteActorStateResponse{}, err
	}

	actorID := in.ActorId
	key := in.Key

	hosted := a.Actors.IsActorHosted(ctx, &actors.ActorHostedRequest{
		ActorType: in.ActorType,
		ActorID:   actorID,
	})

	if !hosted {
		err := status.Errorf(codes.Internal, messages.ErrActorInstanceMissing)
		a.Logger.Debug(err)
		return &runtimev1pb.DeleteActorStateResponse{}, err
	}

	_, err := a.Actors.DeleteState(ctx, &actors.DeleteStateRequest{
		ActorType: in.ActorType,
		ActorID:   actorID,
		Key:       key,
	})
	if err != nil {
		err = status.Errorf(codes.Internal, fmt.Sprintf(messages.ErrActorStateGet, err))
		a.Logger.Debug(err)
		return &runtimev1pb.DeleteActorStateResponse{}, err
	}

	return &runtimev1pb.DeleteActorStateResponse{}, nil
}
