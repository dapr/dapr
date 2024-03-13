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

package fake

import (
	"context"

	componentsapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	"github.com/dapr/dapr/pkg/runtime/hotreload/differ"
	"github.com/dapr/dapr/pkg/runtime/hotreload/loader"
)

type FakeT struct {
	runFn     func(context.Context) error
	component *Fake[componentsapi.Component]
	startFn   func(context.Context) error
}

func New() *FakeT {
	return &FakeT{
		runFn: func(ctx context.Context) error {
			<-ctx.Done()
			return nil
		},
		component: NewFake[componentsapi.Component](),
		startFn: func(ctx context.Context) error {
			<-ctx.Done()
			return nil
		},
	}
}

func (f *FakeT) Run(ctx context.Context) error {
	return f.runFn(ctx)
}

func (f *FakeT) Components() loader.Loader[componentsapi.Component] {
	return f.component
}

func (f *FakeT) WithComponent(fake *Fake[componentsapi.Component]) *FakeT {
	f.component = fake
	return f
}

func (f *FakeT) WithRun(fn func(context.Context) error) *FakeT {
	f.runFn = fn
	return f
}

type Fake[T differ.Resource] struct {
	listFn   func(context.Context) (*differ.LocalRemoteResources[T], error)
	streamFn func(context.Context) (*loader.StreamConn[T], error)
}

func NewFake[T differ.Resource]() *Fake[T] {
	return &Fake[T]{
		listFn: func(context.Context) (*differ.LocalRemoteResources[T], error) {
			return nil, nil
		},
		streamFn: func(context.Context) (*loader.StreamConn[T], error) {
			return &loader.StreamConn[T]{
				EventCh:     make(chan *loader.Event[T]),
				ReconcileCh: make(chan struct{}),
			}, nil
		},
	}
}

func (f *Fake[T]) WithList(fn func(context.Context) (*differ.LocalRemoteResources[T], error)) *Fake[T] {
	f.listFn = fn
	return f
}

func (f *Fake[T]) WithStream(fn func(context.Context) (*loader.StreamConn[T], error)) *Fake[T] {
	f.streamFn = fn
	return f
}

func (f *Fake[T]) List(ctx context.Context) (*differ.LocalRemoteResources[T], error) {
	return f.listFn(ctx)
}

func (f *Fake[T]) Stream(ctx context.Context) (*loader.StreamConn[T], error) {
	return f.streamFn(ctx)
}
