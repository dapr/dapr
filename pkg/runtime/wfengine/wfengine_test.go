/*
Copyright 2022 The Dapr Authors
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

// wfengine_test is a suite of integration tests that verify workflow
// engine behavior using only exported APIs.
package wfengine_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc"

	"github.com/dapr/components-contrib/state"
	"github.com/dapr/dapr/pkg/actors"
	"github.com/dapr/dapr/pkg/config"
	"github.com/dapr/dapr/pkg/runtime/wfengine"
)

// The fake state store code was copied from actors_test.go.
// TODO: Find a way to share the code instead of copying it, if it makes sense to do so.

type fakeStateStoreItem struct {
	data []byte
	etag *string
}

type fakeStateStore struct {
	items map[string]*fakeStateStoreItem
	lock  *sync.RWMutex
}

func fakeStore() state.Store {
	return &fakeStateStore{
		items: map[string]*fakeStateStoreItem{},
		lock:  &sync.RWMutex{},
	}
}

func (f *fakeStateStore) newItem(data []byte) *fakeStateStoreItem {
	etag, _ := uuid.NewRandom()
	etagString := etag.String()
	return &fakeStateStoreItem{
		data: data,
		etag: &etagString,
	}
}

func (f *fakeStateStore) Init(metadata state.Metadata) error {
	return nil
}

func (f *fakeStateStore) Ping() error {
	return nil
}

func (f *fakeStateStore) Features() []state.Feature {
	return []state.Feature{state.FeatureETag, state.FeatureTransactional}
}

func (f *fakeStateStore) Delete(req *state.DeleteRequest) error {
	f.lock.Lock()
	defer f.lock.Unlock()
	delete(f.items, req.Key)

	return nil
}

func (f *fakeStateStore) BulkDelete(req []state.DeleteRequest) error {
	return nil
}

func (f *fakeStateStore) Get(req *state.GetRequest) (*state.GetResponse, error) {
	f.lock.RLock()
	defer f.lock.RUnlock()
	item := f.items[req.Key]

	if item == nil {
		return &state.GetResponse{Data: nil, ETag: nil}, nil
	}

	return &state.GetResponse{Data: item.data, ETag: item.etag}, nil
}

func (f *fakeStateStore) BulkGet(req []state.GetRequest) (bool, []state.BulkGetResponse, error) {
	res := []state.BulkGetResponse{}
	for _, oneRequest := range req {
		oneResponse, err := f.Get(&state.GetRequest{
			Key:      oneRequest.Key,
			Metadata: oneRequest.Metadata,
			Options:  oneRequest.Options,
		})
		if err != nil {
			return false, nil, err
		}

		res = append(res, state.BulkGetResponse{
			Key:  oneRequest.Key,
			Data: oneResponse.Data,
			ETag: oneResponse.ETag,
		})
	}

	return true, res, nil
}

func (f *fakeStateStore) Set(req *state.SetRequest) error {
	b, _ := json.Marshal(&req.Value)
	f.lock.Lock()
	defer f.lock.Unlock()
	f.items[req.Key] = f.newItem(b)

	return nil
}

func (f *fakeStateStore) BulkSet(req []state.SetRequest) error {
	return nil
}

func (f *fakeStateStore) Multi(request *state.TransactionalStateRequest) error {
	f.lock.Lock()
	defer f.lock.Unlock()
	// First we check all eTags
	for _, o := range request.Operations {
		var eTag *string
		key := ""
		if o.Operation == state.Upsert {
			key = o.Request.(state.SetRequest).Key
			eTag = o.Request.(state.SetRequest).ETag
		} else if o.Operation == state.Delete {
			key = o.Request.(state.DeleteRequest).Key
			eTag = o.Request.(state.DeleteRequest).ETag
		}
		item := f.items[key]
		if eTag != nil && item != nil {
			if *eTag != *item.etag {
				return fmt.Errorf("etag does not match for key %v", key)
			}
		}
		if eTag != nil && item == nil {
			return fmt.Errorf("etag does not match for key not found %v", key)
		}
	}

	// Now we can perform the operation.
	for _, o := range request.Operations {
		if o.Operation == state.Upsert {
			req := o.Request.(state.SetRequest)
			b, _ := json.Marshal(req.Value)
			f.items[req.Key] = f.newItem(b)
		} else if o.Operation == state.Delete {
			req := o.Request.(state.DeleteRequest)
			delete(f.items, req.Key)
		}
	}

	return nil
}

// TestStartWorkflowEngine validates that starting the workflow engine returns no errors.
func TestStartWorkflowEngine(t *testing.T) {
	ctx := context.Background()
	engine := wfengine.NewWorkflowEngine()
	store := fakeStore()
	cfg := actors.NewConfig(actors.ConfigOpts{
		AppID:              "wf-app",
		PlacementAddresses: []string{"placement:5050"},
		AppConfig:          config.ApplicationConfig{},
	})
	actors := actors.NewActors(actors.ActorsOpts{
		StateStore:     store,
		Config:         cfg,
		StateStoreName: "workflowStore",
		InternalActors: engine.InternalActors(),
	})
	engine.SetActorRuntime(actors)

	grpcServer := grpc.NewServer()
	engine.ConfigureGrpc(grpcServer)
	err := engine.Start(ctx)
	assert.NoError(t, err)
}

// TODO: Integration tests that actually run workflows (requires a Go SDK of some kind)
