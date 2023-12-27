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

package inmemory

import (
	"context"
	"io"
	"sync"
	"testing"

	"github.com/dapr/components-contrib/state"
	inmemory "github.com/dapr/components-contrib/state/in-memory"
	"github.com/dapr/kit/logger"
)

// Option is a function that configures the process.
type Option func(*options)

// Wrapped is a wrapper around inmemory state store to ensure that Init
// and Close are called only once.
type Wrapped struct {
	state.Store
	features  []state.Feature
	lock      sync.Mutex
	hasInit   bool
	hasClosed bool
}

type WrappedTransactionalMultiMaxSize struct {
	*Wrapped
	state.TransactionalStore
	transactionalStoreMultiMaxSizeFn func() int
}

type WrappedQuerier struct {
	*Wrapped
	queryFunc func(context.Context, *state.QueryRequest) (*state.QueryResponse, error)
}

func New(t *testing.T, fopts ...Option) state.Store {
	opts := options{
		features: []state.Feature{state.FeatureETag, state.FeatureTransactional, state.FeatureTTL},
	}
	for _, fopt := range fopts {
		fopt(&opts)
	}

	impl := inmemory.NewInMemoryStateStore(logger.NewLogger(t.Name() + "_state_store"))
	return &Wrapped{
		Store:    impl,
		features: opts.features,
	}
}

func NewQuerier(t *testing.T, fopts ...Option) state.Store {
	opts := options{
		features: []state.Feature{state.FeatureETag, state.FeatureTransactional, state.FeatureTTL, state.FeatureQueryAPI},
	}
	for _, fopt := range fopts {
		fopt(&opts)
	}

	impl := inmemory.NewInMemoryStateStore(logger.NewLogger(t.Name() + "_state_store"))
	return &WrappedQuerier{
		Wrapped:   &Wrapped{Store: impl, features: opts.features},
		queryFunc: opts.queryFunc,
	}
}

func NewTransactionalMultiMaxSize(t *testing.T, fopts ...Option) state.Store {
	opts := options{
		features: []state.Feature{state.FeatureETag, state.FeatureTransactional, state.FeatureTTL},
	}
	for _, fopt := range fopts {
		fopt(&opts)
	}

	impl := inmemory.NewInMemoryStateStore(logger.NewLogger(t.Name() + "_state_store"))
	return &WrappedTransactionalMultiMaxSize{
		Wrapped:                          &Wrapped{Store: impl, features: opts.features},
		TransactionalStore:               impl.(state.TransactionalStore),
		transactionalStoreMultiMaxSizeFn: opts.transactionalStoreMultiMaxSizeFn,
	}
}

func (w *Wrapped) Init(ctx context.Context, metadata state.Metadata) error {
	w.lock.Lock()
	defer w.lock.Unlock()
	if !w.hasInit {
		w.hasInit = true
		return w.Store.Init(ctx, metadata)
	}
	return nil
}

func (w *WrappedQuerier) Query(ctx context.Context, req *state.QueryRequest) (*state.QueryResponse, error) {
	w.lock.Lock()
	defer w.lock.Unlock()
	if w.queryFunc != nil {
		return w.queryFunc(ctx, req)
	}
	return nil, nil
}

func (w *WrappedTransactionalMultiMaxSize) MultiMaxSize() int {
	w.lock.Lock()
	defer w.lock.Unlock()
	if w.transactionalStoreMultiMaxSizeFn != nil {
		return w.transactionalStoreMultiMaxSizeFn()
	}
	return -1
}

func (w *Wrapped) Close() error {
	w.lock.Lock()
	defer w.lock.Unlock()
	if !w.hasClosed {
		w.hasClosed = true
		return w.Store.(io.Closer).Close()
	}
	return nil
}

func (w *Wrapped) Features() []state.Feature {
	return w.features
}
