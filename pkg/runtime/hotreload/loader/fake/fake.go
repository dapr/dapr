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
	component *Fake[componentsapi.Component]
}

func New() *FakeT {
	return &FakeT{
		component: NewFake[componentsapi.Component](),
	}
}

func (f *FakeT) Close() error {
	return nil
}

func (f *FakeT) Components() loader.Loader[componentsapi.Component] {
	return f.component
}

func (f *FakeT) WithComponent(fake *Fake[componentsapi.Component]) *FakeT {
	f.component = fake
	return f
}

type Fake[T differ.Resource] struct {
	listFn   func(context.Context) (*differ.LocalRemoteResources[T], error)
	streamFn func(context.Context) (<-chan *loader.Event[T], error)
}

func NewFake[T differ.Resource]() *Fake[T] {
	return &Fake[T]{
		listFn: func(context.Context) (*differ.LocalRemoteResources[T], error) {
			return nil, nil
		},
		streamFn: func(context.Context) (<-chan *loader.Event[T], error) {
			return nil, nil
		},
	}
}

func (f *Fake[T]) WithList(fn func(context.Context) (*differ.LocalRemoteResources[T], error)) *Fake[T] {
	f.listFn = fn
	return f
}

func (f *Fake[T]) WithStream(fn func(context.Context) (<-chan *loader.Event[T], error)) *Fake[T] {
	f.streamFn = fn
	return f
}

func (f *Fake[T]) List(ctx context.Context) (*differ.LocalRemoteResources[T], error) {
	return f.listFn(ctx)
}

func (f *Fake[T]) Stream(ctx context.Context) (<-chan *loader.Event[T], error) {
	return f.streamFn(ctx)
}
