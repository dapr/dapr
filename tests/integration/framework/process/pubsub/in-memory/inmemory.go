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

	"github.com/dapr/components-contrib/pubsub"
	inmemory "github.com/dapr/components-contrib/pubsub/in-memory"
	"github.com/dapr/kit/logger"
)

// Option is a function that configures the process.
type Option func(*options)

// Wrapped is a wrapper around inmemory pubsub to ensure that Init
// and Close are called only once.
type WrappedInMemory struct {
	pubsub.PubSub
	features  []pubsub.Feature
	lock      sync.Mutex
	hasInit   bool
	hasClosed bool
	publishFn func(ctx context.Context, req *pubsub.PublishRequest) error
}

func NewWrappedInMemory(t *testing.T, fopts ...Option) pubsub.PubSub {
	opts := options{
		features: []pubsub.Feature{pubsub.FeatureBulkPublish, pubsub.FeatureMessageTTL, pubsub.FeatureSubscribeWildcards},
	}

	for _, fopt := range fopts {
		fopt(&opts)
	}

	impl := inmemory.New(logger.NewLogger(t.Name() + "_pubsub"))
	return &WrappedInMemory{
		PubSub:    impl,
		features:  opts.features,
		publishFn: opts.publishFn,
	}
}

func (w *WrappedInMemory) Publish(ctx context.Context, req *pubsub.PublishRequest) error {
	if w.publishFn != nil {
		return w.publishFn(ctx, req)
	}
	return w.PubSub.Publish(ctx, req)
}

func (w *WrappedInMemory) Init(ctx context.Context, metadata pubsub.Metadata) error {
	w.lock.Lock()
	defer w.lock.Unlock()
	if !w.hasInit {
		w.hasInit = true
		return w.PubSub.Init(ctx, metadata)
	}
	return nil
}

func (w *WrappedInMemory) Close() error {
	w.lock.Lock()
	defer w.lock.Unlock()
	if !w.hasClosed {
		w.hasClosed = true
		return w.PubSub.(io.Closer).Close()
	}
	return nil
}
