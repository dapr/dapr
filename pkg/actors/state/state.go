/*
Copyright 2024 The Dapr Authors
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

package state

import (
	"context"
	"errors"
	"fmt"
	"strings"

	contribstate "github.com/dapr/components-contrib/state"
	"github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/internal/key"
	"github.com/dapr/dapr/pkg/actors/internal/placement"
	"github.com/dapr/dapr/pkg/actors/table"
	"github.com/dapr/dapr/pkg/messages"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/runtime/compstore"
)

const (
	metadataPartitionKey = "partitionKey"

	errStateStoreNotFound      = "actors: state store does not exist or incorrectly configured"
	errStateStoreNotConfigured = `actors: state store does not exist or incorrectly configured. Have you set the property '{"name": "actorStateStore", "value": "true"}' in your state store component file?`
)

var ErrTransactionsTooManyOperations = errors.New("the transaction contains more operations than supported by the state store")

type Interface interface {
	// Get retrieves actor state.
	Get(ctx context.Context, req *api.GetStateRequest) (*api.StateResponse, error)

	// GetBulk retrieves actor state in bulk.
	GetBulk(ctx context.Context, req *api.GetBulkStateRequest) (api.BulkStateResponse, error)

	// TransactionalStateOperation performs a transactional state operation with the actor state store.
	TransactionalStateOperation(ctx context.Context, ignoreHosted bool, req *api.TransactionalRequest) error
}

type Backend interface {
	contribstate.Store
	contribstate.TransactionalStore
}

type Options struct {
	AppID      string
	StoreName  string
	CompStore  *compstore.ComponentStore
	Resiliency resiliency.Provider
	Table      table.Interface
	Placement  placement.Interface

	// TODO: @joshvanl Remove in Dapr 1.12 when ActorStateTTL is finalized.
	StateTTLEnabled bool
}

type state struct {
	appID      string
	storeName  string
	compStore  *compstore.ComponentStore
	resiliency resiliency.Provider
	table      table.Interface
	placement  placement.Interface

	// TODO: @joshvanl Remove in Dapr 1.12 when ActorStateTTL is finalized.
	stateTTLEnabled bool
}

func New(opts Options) Interface {
	return &state{
		appID:           opts.AppID,
		storeName:       opts.StoreName,
		compStore:       opts.CompStore,
		resiliency:      opts.Resiliency,
		table:           opts.Table,
		placement:       opts.Placement,
		stateTTLEnabled: opts.StateTTLEnabled,
	}
}

func (s *state) Get(ctx context.Context, req *api.GetStateRequest) (*api.StateResponse, error) {
	ctx, cancel, err := s.placement.Lock(ctx)
	if err != nil {
		return nil, err
	}
	defer cancel()

	storeName, store, err := s.stateStore()
	if err != nil {
		return nil, err
	}

	actorKey := req.ActorKey()
	partitionKey := key.ConstructComposite(s.appID, actorKey)
	metadata := map[string]string{metadataPartitionKey: partitionKey}

	key := s.constructActorStateKey(actorKey, req.Key)

	policyRunner := resiliency.NewRunner[*contribstate.GetResponse](ctx,
		s.resiliency.ComponentOutboundPolicy(storeName, resiliency.Statestore),
	)
	storeReq := &contribstate.GetRequest{
		Key:      key,
		Metadata: metadata,
	}

	resp, err := policyRunner(func(ctx context.Context) (*contribstate.GetResponse, error) {
		return store.Get(ctx, storeReq)
	})
	if err != nil {
		return nil, err
	}

	if resp == nil {
		return &api.StateResponse{}, nil
	}

	return &api.StateResponse{
		Data:     resp.Data,
		Metadata: resp.Metadata,
	}, nil
}

func (s *state) GetBulk(ctx context.Context, req *api.GetBulkStateRequest) (api.BulkStateResponse, error) {
	ctx, cancel, err := s.placement.Lock(ctx)
	if err != nil {
		return nil, err
	}
	defer cancel()

	storeName, store, err := s.stateStore()
	if err != nil {
		return nil, err
	}

	actorKey := req.ActorKey()
	baseKey := key.ConstructComposite(s.appID, actorKey)
	metadata := map[string]string{metadataPartitionKey: baseKey}

	bulkReqs := make([]contribstate.GetRequest, len(req.Keys))
	for i, key := range req.Keys {
		bulkReqs[i] = contribstate.GetRequest{
			Key:      s.constructActorStateKey(actorKey, key),
			Metadata: metadata,
		}
	}

	policyRunner := resiliency.NewRunner[[]contribstate.BulkGetResponse](ctx,
		s.resiliency.ComponentOutboundPolicy(storeName, resiliency.Statestore),
	)
	res, err := policyRunner(func(ctx context.Context) ([]contribstate.BulkGetResponse, error) {
		return store.BulkGet(ctx, bulkReqs, contribstate.BulkGetOpts{})
	})
	if err != nil {
		return nil, err
	}

	// Add the dapr separator to baseKey
	baseKey += api.DaprSeparator

	bulkRes := make(api.BulkStateResponse, len(res))
	for _, r := range res {
		if r.Error != "" {
			return nil, fmt.Errorf("failed to retrieve key '%s': %s", r.Key, r.Error)
		}

		// Trim the prefix from the key
		bulkRes[strings.TrimPrefix(r.Key, baseKey)] = r.Data
	}

	return bulkRes, nil
}

func (s *state) TransactionalStateOperation(ctx context.Context, ignoreHosted bool, req *api.TransactionalRequest) error {
	ctx, cancel, err := s.placement.Lock(ctx)
	if err != nil {
		return err
	}
	defer cancel()

	if !ignoreHosted {
		if _, ok := s.table.HostedTarget(req.ActorType, req.ActorID); !ok {
			return messages.ErrActorInstanceMissing
		}
	}

	operations := make([]contribstate.TransactionalStateOperation, len(req.Operations))
	baseKey := key.ConstructComposite(s.appID, req.ActorKey())
	metadata := map[string]string{metadataPartitionKey: baseKey}
	baseKey += api.DaprSeparator
	for i, o := range req.Operations {
		operations[i], err = o.StateOperation(baseKey, api.StateOperationOpts{
			Metadata: metadata,
			// TODO: @joshvanl Remove in Dapr 1.12 when ActorStateTTL is finalized.
			StateTTLEnabled: s.stateTTLEnabled,
		})
		if err != nil {
			return err
		}
	}

	return s.executeStateStoreTransaction(ctx, operations, metadata)
}

func (s *state) executeStateStoreTransaction(ctx context.Context, operations []contribstate.TransactionalStateOperation, metadata map[string]string) error {
	storeName, store, err := s.stateStore()
	if err != nil {
		return err
	}

	if maxMulti, ok := store.(contribstate.TransactionalStoreMultiMaxSize); ok {
		max := maxMulti.MultiMaxSize()
		if max > 0 && len(operations) > max {
			return ErrTransactionsTooManyOperations
		}
	}
	stateReq := &contribstate.TransactionalStateRequest{
		Operations: operations,
		Metadata:   metadata,
	}
	policyRunner := resiliency.NewRunner[struct{}](ctx,
		s.resiliency.ComponentOutboundPolicy(storeName, resiliency.Statestore),
	)
	_, err = policyRunner(func(ctx context.Context) (struct{}, error) {
		return struct{}{}, store.Multi(ctx, stateReq)
	})
	return err
}

func (s *state) stateStore() (string, Backend, error) {
	storeS, ok := s.compStore.GetStateStore(s.storeName)
	if !ok {
		return "", nil, errors.New(errStateStoreNotFound)
	}

	store, ok := storeS.(Backend)
	if !ok || !contribstate.FeatureETag.IsPresent(store.Features()) || !contribstate.FeatureTransactional.IsPresent(store.Features()) {
		return "", nil, errors.New(errStateStoreNotConfigured)
	}

	return s.storeName, store, nil
}

func (s *state) constructActorStateKey(actorKey, actorID string) string {
	return key.ConstructComposite(s.appID, actorKey, actorID)
}
