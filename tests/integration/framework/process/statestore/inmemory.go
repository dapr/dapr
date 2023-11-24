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

package statestore

import (
	"context"
	"io"
	"sync"
	"testing"

	"github.com/dapr/components-contrib/state"
	inmemory "github.com/dapr/components-contrib/state/in-memory"
	"github.com/dapr/kit/logger"
)

// WrappedInMemory is a wrapper around inmemory state store to ensure that Init
// and Close are called only once.
type WrappedInMemory struct {
	state.Store
	state.TransactionalStore
	lock      sync.Mutex
	hasInit   bool
	hasClosed bool
}

func NewWrappedInMemory(t *testing.T) state.Store {
	impl := inmemory.NewInMemoryStateStore(logger.NewLogger(t.Name() + "_state_store"))
	return &WrappedInMemory{
		Store:              impl,
		TransactionalStore: impl.(state.TransactionalStore),
	}
}

func (w *WrappedInMemory) Init(ctx context.Context, metadata state.Metadata) error {
	w.lock.Lock()
	defer w.lock.Unlock()
	if !w.hasInit {
		w.hasInit = true
		return w.Store.Init(ctx, metadata)
	}
	return nil
}

func (w *WrappedInMemory) Close() error {
	w.lock.Lock()
	defer w.lock.Unlock()
	if !w.hasClosed {
		w.hasClosed = true
		return w.Store.(io.Closer).Close()
	}
	return nil
}
