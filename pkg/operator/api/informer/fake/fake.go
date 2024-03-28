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

package fake

import (
	"context"

	"github.com/dapr/dapr/pkg/operator/api/informer"
	"github.com/dapr/dapr/pkg/runtime/meta"
)

type Fake[T meta.Resource] struct {
	runFn          func(context.Context) error
	watchUpdatesFn func(context.Context, string) (<-chan *informer.Event[T], error)
}

func New[T meta.Resource]() *Fake[T] {
	return &Fake[T]{
		runFn: func(context.Context) error {
			return nil
		},
		watchUpdatesFn: func(context.Context, string) (<-chan *informer.Event[T], error) {
			return nil, nil
		},
	}
}

func (f *Fake[T]) WithRun(fn func(context.Context) error) *Fake[T] {
	f.runFn = fn
	return f
}

func (f *Fake[T]) WithWatchUpdates(fn func(context.Context, string) (<-chan *informer.Event[T], error)) *Fake[T] {
	f.watchUpdatesFn = fn
	return f
}

func (f *Fake[T]) Run(ctx context.Context) error {
	return f.runFn(ctx)
}

func (f *Fake[T]) WatchUpdates(ctx context.Context, namespace string) (<-chan *informer.Event[T], error) {
	return f.watchUpdatesFn(ctx, namespace)
}
