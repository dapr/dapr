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
	"sync"

	"github.com/cenkalti/backoff/v4"

	contribstate "github.com/dapr/components-contrib/state"
	"github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/internal/key"
	"github.com/dapr/dapr/pkg/actors/internal/placement"
	"github.com/dapr/dapr/pkg/actors/table"
	"github.com/dapr/dapr/pkg/messages"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.runtime.actors.state")

const (
	metadataPartitionKey = "partitionKey"

	errStateStoreNotConfigured = `actors: state store does not exist or incorrectly configured. Have you set the property '{"name": "actorStateStore", "value": "true"}' in your state store component file?`
)

var ErrTransactionsTooManyOperations = errors.New("the transaction contains more operations than supported by the state store")

type Interface interface {
	// Get retrieves actor state.
	Get(ctx context.Context, req *api.GetStateRequest, lock bool) (*api.StateResponse, error)

	// GetBulk retrieves actor state in bulk.
	GetBulk(ctx context.Context, req *api.GetBulkStateRequest, lock bool) (api.BulkStateResponse, error)

	// TransactionalStateOperation performs a transactional state operation with the actor state store.
	TransactionalStateOperation(ctx context.Context, ignoreHosted bool, req *api.TransactionalRequest, lock bool) error

	// TransactionalStateOperationWithETags returns new etags by key, empty unless the store is strict.
	TransactionalStateOperationWithETags(ctx context.Context, ignoreHosted bool, req *api.TransactionalRequest, lock bool) (map[string]string, error)
}

type Backend interface {
	contribstate.Store
	contribstate.TransactionalStore
}

type Options struct {
	AppID      string
	CompStore  *compstore.ComponentStore
	Resiliency resiliency.Provider
	Table      table.Interface
	Placement  placement.Interface

	// TODO: @joshvanl Remove in Dapr 1.12 when ActorStateTTL is finalized.
	StateTTLEnabled bool
}

type state struct {
	appID      string
	compStore  *compstore.ComponentStore
	resiliency resiliency.Provider
	table      table.Interface
	placement  placement.Interface

	missingETagLogged  sync.Map
	rejectedETagLogged sync.Map

	// TODO: @joshvanl Remove in Dapr 1.12 when ActorStateTTL is finalized.
	stateTTLEnabled bool
}

func New(opts Options) Interface {
	return &state{
		appID:           opts.AppID,
		compStore:       opts.CompStore,
		resiliency:      opts.Resiliency,
		table:           opts.Table,
		placement:       opts.Placement,
		stateTTLEnabled: opts.StateTTLEnabled,
	}
}

func (s *state) Get(ctx context.Context, req *api.GetStateRequest, lock bool) (*api.StateResponse, error) {
	if lock {
		var cancel context.CancelCauseFunc
		var err error
		ctx, cancel, err = s.placement.Lock(ctx, req.ActorType)
		if err != nil {
			return nil, err
		}
		defer cancel(nil)
	}

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

	etag := resp.ETag
	if req.StrictETag {
		etag = s.strictETag(storeName, store, req.ActorType, resp)
	}

	return &api.StateResponse{
		Data:     resp.Data,
		Metadata: resp.Metadata,
		ETag:     etag,
	}, nil
}

func (s *state) strictETag(storeName string, store Backend, actorType string, resp *contribstate.GetResponse) *string {
	if !supportOf(store).strict {
		return nil
	}
	if resp.ETag == nil || *resp.ETag == "" {
		if len(resp.Data) > 0 {
			s.logMissingETag(storeName, actorType, "read")
		}
		return nil
	}
	return resp.ETag
}

func (s *state) logMissingETag(storeName, actorType, op string) {
	if _, logged := s.missingETagLogged.LoadOrStore(op+"/"+storeName+"/"+actorType, struct{}{}); !logged {
		log.Errorf("Actor state store '%s' advertises %s but returned no etag on a %s for actor type '%s'. The state is returned without an etag, so the next save of that key isn't guarded. Logged once per actor type", storeName, contribstate.FeatureTransactionalETags, op, actorType)
	}
}

func (s *state) GetBulk(ctx context.Context, req *api.GetBulkStateRequest, lock bool) (api.BulkStateResponse, error) {
	if lock {
		var cancel context.CancelCauseFunc
		var err error
		ctx, cancel, err = s.placement.Lock(ctx, req.ActorType)
		if err != nil {
			return nil, err
		}
		defer cancel(nil)
	}

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
		bulkRes[strings.TrimPrefix(r.Key, baseKey)] = api.BulkStateEntry{
			Data: r.Data,
			ETag: r.ETag,
		}
	}

	return bulkRes, nil
}

func (s *state) TransactionalStateOperation(ctx context.Context, ignoreHosted bool, req *api.TransactionalRequest, lock bool) error {
	_, err := s.transactionalStateOperation(ctx, ignoreHosted, req, lock, false)
	return err
}

func (s *state) TransactionalStateOperationWithETags(ctx context.Context, ignoreHosted bool, req *api.TransactionalRequest, lock bool) (map[string]string, error) {
	return s.transactionalStateOperation(ctx, ignoreHosted, req, lock, true)
}

func (s *state) transactionalStateOperation(ctx context.Context, ignoreHosted bool, req *api.TransactionalRequest, lock bool, returnETags bool) (map[string]string, error) {
	if lock {
		var cancel context.CancelCauseFunc
		var err error
		ctx, cancel, err = s.placement.Lock(ctx, req.ActorType)
		if err != nil {
			return nil, err
		}
		defer cancel(nil)
	}

	if !ignoreHosted && !s.table.ActorExists(req.ActorType, req.ActorID) {
		return nil, messages.ErrActorInstanceMissing
	}

	operations := make([]contribstate.TransactionalStateOperation, len(req.Operations))
	baseKey := key.ConstructComposite(s.appID, req.ActorKey())
	metadata := map[string]string{metadataPartitionKey: baseKey}
	baseKey += api.DaprSeparator
	var err error
	for i, o := range req.Operations {
		operations[i], err = o.StateOperation(baseKey, api.StateOperationOpts{
			Metadata: metadata,
			// TODO: @joshvanl Remove in Dapr 1.12 when ActorStateTTL is finalized.
			StateTTLEnabled: s.stateTTLEnabled,
			FirstWriteETag:  req.FirstWriteETag,
		})
		if err != nil {
			if req.FirstWriteETag {
				return nil, toETagAPIError(err)
			}
			return nil, err
		}
	}

	storeName, etags, err := s.executeStateStoreTransaction(ctx, req.ActorType, operations, metadata, req.FirstWriteETag, returnETags)
	if req.FirstWriteETag {
		err = toETagAPIError(err)
	}
	if err != nil || !returnETags {
		return nil, err
	}

	res := make(map[string]string, len(etags))
	if etags == nil {
		return res, nil
	}
	for _, op := range operations {
		switch o := op.(type) {
		case contribstate.SetRequest:
			k := strings.TrimPrefix(o.Key, baseKey)
			if etag := etags[o.Key]; etag != "" {
				res[k] = etag
			} else {
				delete(res, k)
				s.logMissingETag(storeName, req.ActorType, "transaction")
			}
		case contribstate.DeleteRequest:
			delete(res, strings.TrimPrefix(o.Key, baseKey))
		}
	}
	return res, nil
}

func toETagAPIError(err error) error {
	var etagErr *contribstate.ETagError
	if errors.As(err, &etagErr) {
		switch etagErr.Kind() {
		case contribstate.ETagMismatch:
			return messages.ErrActorStateETagMismatch.WithFormat(err)
		case contribstate.ETagInvalid:
			return messages.ErrActorStateETagInvalid.WithFormat(err)
		}
	}
	return err
}

func (s *state) executeStateStoreTransaction(ctx context.Context, actorType string, operations []contribstate.TransactionalStateOperation, metadata map[string]string, public, returnETags bool) (string, map[string]string, error) {
	storeName, store, err := s.stateStore()
	if err != nil {
		return "", nil, err
	}

	var sup support
	if public || returnETags {
		sup = supportOf(store)
	}
	if public {
		if err = s.check(sup, storeName, actorType, operations); err != nil {
			return storeName, nil, err
		}
	}

	if maxMulti, ok := store.(contribstate.TransactionalStoreMultiMaxSize); ok {
		max := maxMulti.MultiMaxSize()
		if max > 0 && len(operations) > max {
			return storeName, nil, ErrTransactionsTooManyOperations
		}
	}
	stateReq := &contribstate.TransactionalStateRequest{
		Operations: operations,
		Metadata:   metadata,
	}

	var withETags contribstate.TransactionalStoreWithETags
	if returnETags && sup.strict {
		withETags = store.(contribstate.TransactionalStoreWithETags)
	}

	policyRunner := resiliency.NewRunner[map[string]string](ctx,
		s.resiliency.ComponentOutboundPolicy(storeName, resiliency.Statestore),
	)
	etags, err := policyRunner(func(ctx context.Context) (map[string]string, error) {
		var (
			etags map[string]string
			mErr  error
		)
		if withETags != nil {
			var res *contribstate.TransactionalStateResponse
			res, mErr = withETags.MultiWithETags(ctx, stateReq)
			if res != nil {
				etags = res.ETags
			}
			if etags == nil {
				etags = map[string]string{}
			}
		} else {
			mErr = store.Multi(ctx, stateReq)
		}
		if public && errors.As(mErr, new(*contribstate.ETagError)) {
			return nil, backoff.Permanent(mErr)
		}
		return etags, mErr
	})
	return storeName, etags, err
}

// stateStore resolves the actor state store from the component store on
// every call, so the store can be hot reloaded (added, replaced, or removed)
// at any point in the runtime's lifetime.
func (s *state) stateStore() (string, Backend, error) {
	storeS, storeName, ok := s.compStore.GetStateStoreActor()
	if !ok {
		return "", nil, messages.ErrActorRuntimeNotFound
	}

	store, ok := storeS.(Backend)
	if !ok || !contribstate.FeatureETag.IsPresent(store.Features()) || !contribstate.FeatureTransactional.IsPresent(store.Features()) {
		return "", nil, errors.New(errStateStoreNotConfigured)
	}

	return storeName, store, nil
}

func (s *state) constructActorStateKey(actorKey, actorID string) string {
	return key.ConstructComposite(s.appID, actorKey, actorID)
}
