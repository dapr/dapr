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

package localfile

import (
	"context"
	"io"
	"sync"
	"testing"

	"github.com/dapr/components-contrib/secretstores"
	localfile "github.com/dapr/components-contrib/secretstores/local/file"
	"github.com/dapr/kit/logger"
)

// Option is a function that configures the process.
type Option func(*options)

// Wrapped is a wrapper around local.file secretstore to ensure that Init
// and Close are called only once.
type Wrapped struct {
	secretstores.SecretStore
	lock      sync.Mutex
	hasInit   bool
	hasClosed bool

	getSecretFn func(ctx context.Context, req secretstores.GetSecretRequest) (secretstores.GetSecretResponse, error)

	bulkGetSecretFn func(ctx context.Context, req secretstores.BulkGetSecretRequest) (secretstores.BulkGetSecretResponse, error)
}

func New(t *testing.T, fopts ...Option) secretstores.SecretStore {
	var opts options
	for _, fopt := range fopts {
		fopt(&opts)
	}

	impl := localfile.NewLocalSecretStore(logger.NewLogger(t.Name() + "_secret_store"))
	return &Wrapped{
		SecretStore:     impl,
		lock:            sync.Mutex{},
		hasInit:         false,
		hasClosed:       false,
		getSecretFn:     opts.getSecretFn,
		bulkGetSecretFn: opts.bulkGetSecretFn,
	}
}

func (w *Wrapped) Init(ctx context.Context, metadata secretstores.Metadata) error {
	w.lock.Lock()
	defer w.lock.Unlock()
	if !w.hasInit {
		w.hasInit = true
		return w.SecretStore.Init(ctx, metadata)
	}
	return nil
}

func (w *Wrapped) GetSecret(ctx context.Context, req secretstores.GetSecretRequest) (secretstores.GetSecretResponse, error) {
	if w.getSecretFn != nil {
		return w.getSecretFn(ctx, req)
	}
	return w.SecretStore.GetSecret(ctx, req)
}

func (w *Wrapped) BulkGetSecret(ctx context.Context, req secretstores.BulkGetSecretRequest) (secretstores.BulkGetSecretResponse, error) {
	if w.bulkGetSecretFn != nil {
		return w.bulkGetSecretFn(ctx, req)
	}
	return w.SecretStore.BulkGetSecret(ctx, req)
}

func (w *Wrapped) Close() error {
	w.lock.Lock()
	defer w.lock.Unlock()
	if !w.hasClosed {
		w.hasClosed = true
		return w.SecretStore.(io.Closer).Close()
	}
	return nil
}
