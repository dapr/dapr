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

package state

import (
	"context"
	"errors"

	"go.opentelemetry.io/otel/attribute"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"

	"github.com/dapr/components-contrib/health"
	"github.com/dapr/components-contrib/state"
	diag "github.com/dapr/dapr/pkg/diagnostics"
)

var errNotImplemented = errors.New("operation not implemented by this state store")

// Unwrapper is implemented by state stores that decorate another store.
type Unwrapper interface {
	Unwrap() state.Store
}

// As returns store as the optional interface T, such as state.Querier, if the
// underlying store implements it. Use it instead of a type assertion: the
// WithTracing wrapper implements every optional interface.
func As[T any](store state.Store) (T, bool) {
	inner := store
	for {
		u, ok := inner.(Unwrapper)
		if !ok {
			break
		}
		inner = u.Unwrap()
	}

	var zero T
	if _, ok := inner.(T); !ok {
		return zero, false
	}
	if v, ok := store.(T); ok {
		return v, true
	}
	return inner.(T), true
}

// WithTracing wraps store so every call into it emits a client span, parented
// to the span in the call's context.
func WithTracing(store state.Store, name, componentType string) state.Store {
	return &tracedStore{
		Store: store,
		info:  diag.ComponentSpanInfo{Name: name, Type: componentType},
	}
}

// tracedStore embeds the store so Init, Close, Features and
// GetComponentMetadata (present only under the metadata build tag) pass
// through unchanged.
type tracedStore struct {
	state.Store
	info diag.ComponentSpanInfo
}

func (t *tracedStore) Unwrap() state.Store {
	return t.Store
}

func (t *tracedStore) Get(ctx context.Context, req *state.GetRequest) (*state.GetResponse, error) {
	ctx, span := diag.StartComponentSpan(ctx, t.info, diag.Get)
	res, err := t.Store.Get(ctx, req)
	diag.EndComponentSpan(span, err)
	return res, err
}

func (t *tracedStore) Set(ctx context.Context, req *state.SetRequest) error {
	ctx, span := diag.StartComponentSpan(ctx, t.info, diag.Set)
	err := t.Store.Set(ctx, req)
	diag.EndComponentSpan(span, err)
	return err
}

func (t *tracedStore) Delete(ctx context.Context, req *state.DeleteRequest) error {
	ctx, span := diag.StartComponentSpan(ctx, t.info, diag.Delete)
	err := t.Store.Delete(ctx, req)
	diag.EndComponentSpan(span, err)
	return err
}

func (t *tracedStore) BulkGet(ctx context.Context, req []state.GetRequest, opts state.BulkGetOpts) ([]state.BulkGetResponse, error) {
	ctx, span := diag.StartComponentSpan(ctx, t.info, diag.BulkGet, batchSize(len(req))...)
	res, err := t.Store.BulkGet(ctx, req, opts)
	diag.EndComponentSpan(span, err)
	return res, err
}

func (t *tracedStore) BulkSet(ctx context.Context, req []state.SetRequest, opts state.BulkStoreOpts) error {
	ctx, span := diag.StartComponentSpan(ctx, t.info, diag.BulkSet, batchSize(len(req))...)
	err := t.Store.BulkSet(ctx, req, opts)
	diag.EndComponentSpan(span, err)
	return err
}

func (t *tracedStore) BulkDelete(ctx context.Context, req []state.DeleteRequest, opts state.BulkStoreOpts) error {
	ctx, span := diag.StartComponentSpan(ctx, t.info, diag.BulkDelete, batchSize(len(req))...)
	err := t.Store.BulkDelete(ctx, req, opts)
	diag.EndComponentSpan(span, err)
	return err
}

func (t *tracedStore) Multi(ctx context.Context, req *state.TransactionalStateRequest) error {
	inner, ok := t.Store.(state.TransactionalStore)
	if !ok {
		return errNotImplemented
	}
	ctx, span := diag.StartComponentSpan(ctx, t.info, diag.StateTransaction, batchSize(len(req.Operations))...)
	err := inner.Multi(ctx, req)
	diag.EndComponentSpan(span, err)
	return err
}

func (t *tracedStore) MultiMaxSize() int {
	if inner, ok := t.Store.(state.TransactionalStoreMultiMaxSize); ok {
		return inner.MultiMaxSize()
	}
	return -1
}

func (t *tracedStore) Query(ctx context.Context, req *state.QueryRequest) (*state.QueryResponse, error) {
	inner, ok := t.Store.(state.Querier)
	if !ok {
		return nil, errNotImplemented
	}
	ctx, span := diag.StartComponentSpan(ctx, t.info, diag.StateQuery)
	res, err := inner.Query(ctx, req)
	diag.EndComponentSpan(span, err)
	return res, err
}

func (t *tracedStore) KeysLike(ctx context.Context, req *state.KeysLikeRequest) (*state.KeysLikeResponse, error) {
	inner, ok := t.Store.(state.KeysLiker)
	if !ok {
		return nil, errNotImplemented
	}
	ctx, span := diag.StartComponentSpan(ctx, t.info, diag.KeysLike)
	res, err := inner.KeysLike(ctx, req)
	diag.EndComponentSpan(span, err)
	return res, err
}

func (t *tracedStore) DeleteWithPrefix(ctx context.Context, req state.DeleteWithPrefixRequest) (state.DeleteWithPrefixResponse, error) {
	inner, ok := t.Store.(state.DeleteWithPrefix)
	if !ok {
		return state.DeleteWithPrefixResponse{}, errNotImplemented
	}
	ctx, span := diag.StartComponentSpan(ctx, t.info, diag.DeleteWithPrefix)
	res, err := inner.DeleteWithPrefix(ctx, req)
	diag.EndComponentSpan(span, err)
	return res, err
}

// Ping is not traced: it is a health probe, not a data operation.
func (t *tracedStore) Ping(ctx context.Context) error {
	if inner, ok := t.Store.(health.Pinger); ok {
		return inner.Ping(ctx)
	}
	return state.ErrPingNotImplemented
}

// batchSize returns the db.operation.batch.size attribute, which semconv only
// allows for batches of two or more operations.
func batchSize(n int) []attribute.KeyValue {
	if n < 2 {
		return nil
	}
	return []attribute.KeyValue{semconv.DBOperationBatchSizeKey.Int(n)}
}
