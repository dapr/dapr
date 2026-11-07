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

package actors

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"

	actorsapi "github.com/dapr/dapr/pkg/actors/api"
	actorsfake "github.com/dapr/dapr/pkg/actors/fake"
	"github.com/dapr/dapr/pkg/actors/router"
	routerfake "github.com/dapr/dapr/pkg/actors/router/fake"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
	"github.com/dapr/durabletask-go/api"
)

// A force purge runs on the daprd that received it. When placement hosts the
// workflow actor on another daprd, that daprd is asked to evict it, or it
// keeps serving the purged instance from its cache.
func TestHaltPurgedActors_evictsRemoteOwner(t *testing.T) {
	const (
		appID      = "myapp"
		actorType  = "dapr.internal.default." + appID + ".workflow"
		instanceID = "wf1"
	)

	newBackend := func(local bool, lookupErr, callErr error) (*Actors, func() []*internalsv1pb.InternalInvokeRequest) {
		var (
			lock  sync.Mutex
			calls []*internalsv1pb.InternalInvokeRequest
		)
		r := routerfake.New().WithCallFn(func(_ context.Context, req *internalsv1pb.InternalInvokeRequest) (*internalsv1pb.InternalInvokeResponse, error) {
			lock.Lock()
			defer lock.Unlock()
			calls = append(calls, req)
			return new(internalsv1pb.InternalInvokeResponse), callErr
		})
		abe := &Actors{
			appID:             appID,
			workflowActorType: actorType,
			actors: actorsfake.New().
				WithRouter(func(context.Context) (router.Interface, error) { return r, nil }).
				WithPlacementLookupActor(func(context.Context, *actorsapi.LookupActorRequest) (*actorsapi.LookupActorResponse, error) {
					if lookupErr != nil {
						return nil, lookupErr
					}
					return &actorsapi.LookupActorResponse{Local: local}, nil
				}),
		}
		return abe, func() []*internalsv1pb.InternalInvokeRequest {
			lock.Lock()
			defer lock.Unlock()
			return calls
		}
	}

	t.Run("owner on another daprd is sent an eviction", func(t *testing.T) {
		abe, calls := newBackend(false, nil, nil)
		abe.haltPurgedActors(t.Context(), api.InstanceID(instanceID))

		got := calls()
		if assert.Len(t, got, 1) {
			assert.Equal(t, todo.EvictWorkflowMethod, got[0].GetMessage().GetMethod())
			assert.Equal(t, actorType, got[0].GetActor().GetActorType())
			assert.Equal(t, instanceID, got[0].GetActor().GetActorId())
		}
	})

	t.Run("owner on this daprd is halted locally only", func(t *testing.T) {
		abe, calls := newBackend(true, nil, nil)
		abe.haltPurgedActors(t.Context(), api.InstanceID(instanceID))
		assert.Empty(t, calls())
	})

	t.Run("a failed lookup does not block the purge", func(t *testing.T) {
		abe, calls := newBackend(false, errors.New("placement unavailable"), nil)
		abe.haltPurgedActors(t.Context(), api.InstanceID(instanceID))
		assert.Empty(t, calls())
	})

	t.Run("an owner that refuses the eviction does not block the purge", func(t *testing.T) {
		abe, calls := newBackend(false, nil, errors.New("no such method: "+todo.EvictWorkflowMethod))
		abe.haltPurgedActors(t.Context(), api.InstanceID(instanceID))
		assert.Len(t, calls(), 1)
	})

	t.Run("no placement means no other daprd to evict", func(t *testing.T) {
		abe := &Actors{appID: appID, workflowActorType: actorType, actors: actorsfake.New()}
		abe.haltPurgedActors(t.Context(), api.InstanceID(instanceID))
	})
}
