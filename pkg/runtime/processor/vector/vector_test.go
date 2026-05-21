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

package vector

import (
	"context"
	"testing"

	contribvector "github.com/dapr/components-contrib/vector"
	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	compvector "github.com/dapr/dapr/pkg/components/vector"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	"github.com/dapr/dapr/pkg/runtime/meta"
	"github.com/dapr/kit/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type fakeVectorComponent struct {
	contribvector.Vector
	initErr     error
	closeErr    error
	initCalled  bool
	closeCalled bool
	metadata    contribvector.Metadata
}

func (f *fakeVectorComponent) Init(_ context.Context, metadata contribvector.Metadata) error {
	f.initCalled = true
	f.metadata = metadata
	return f.initErr
}

func (f *fakeVectorComponent) Close() error {
	f.closeCalled = true
	return f.closeErr
}

func TestNew(t *testing.T) {
	t.Parallel()

	registry := compvector.NewRegistry()
	store := compstore.New()
	metadata := meta.New(meta.Options{})

	processor := New(Options{
		Registry: registry,
		Store:    store,
		Meta:     metadata,
	})

	require.NotNil(t, processor)
	assert.Same(t, registry, processor.registry)
	assert.Same(t, store, processor.store)
	assert.Same(t, metadata, processor.meta)
}

func TestInit(t *testing.T) {
	t.Parallel()

	t.Run("initializes component and registers in compstore", func(t *testing.T) {
		t.Parallel()

		component := newVectorComponent()
		fake := &fakeVectorComponent{}
		registry := compvector.NewRegistry()
		registry.RegisterComponent(func(_ logger.Logger) contribvector.Vector {
			return fake
		}, "mock")
		store := compstore.New()
		processor := New(Options{
			Registry: registry,
			Store:    store,
			Meta:     meta.New(meta.Options{}),
		})

		err := processor.Init(t.Context(), component)

		require.NoError(t, err)
		assert.True(t, fake.initCalled)
		assert.Equal(t, component.Name, fake.metadata.Name)
		got, ok := store.GetVector(component.Name)
		require.True(t, ok)
		assert.Same(t, fake, got)
	})

	t.Run("returns create error", func(t *testing.T) {
		t.Parallel()

		component := newVectorComponent()
		store := compstore.New()
		processor := New(Options{
			Registry: compvector.NewRegistry(),
			Store:    store,
			Meta:     meta.New(meta.Options{}),
		})

		err := processor.Init(t.Context(), component)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "couldn't find vector vector.mock/v1")
		_, ok := store.GetVector(component.Name)
		assert.False(t, ok)
	})

	t.Run("returns init error without registering", func(t *testing.T) {
		t.Parallel()

		component := newVectorComponent()
		fake := &fakeVectorComponent{initErr: assert.AnError}
		registry := compvector.NewRegistry()
		registry.RegisterComponent(func(_ logger.Logger) contribvector.Vector {
			return fake
		}, "mock")
		store := compstore.New()
		processor := New(Options{
			Registry: registry,
			Store:    store,
			Meta:     meta.New(meta.Options{}),
		})

		err := processor.Init(t.Context(), component)

		require.Error(t, err)
		assert.Contains(t, err.Error(), assert.AnError.Error())
		assert.True(t, fake.initCalled)
		_, ok := store.GetVector(component.Name)
		assert.False(t, ok)
	})
}

func TestClose(t *testing.T) {
	t.Parallel()

	t.Run("closes and unregisters component", func(t *testing.T) {
		t.Parallel()

		component := newVectorComponent()
		fake := &fakeVectorComponent{}
		store := compstore.New()
		store.AddVector(component.Name, fake)
		processor := New(Options{Store: store})

		err := processor.Close(component)

		require.NoError(t, err)
		assert.True(t, fake.closeCalled)
		_, ok := store.GetVector(component.Name)
		assert.False(t, ok)
	})

	t.Run("ignores unknown component", func(t *testing.T) {
		t.Parallel()

		processor := New(Options{Store: compstore.New()})

		err := processor.Close(newVectorComponent())

		require.NoError(t, err)
	})

	t.Run("returns close error and unregisters component", func(t *testing.T) {
		t.Parallel()

		component := newVectorComponent()
		fake := &fakeVectorComponent{closeErr: assert.AnError}
		store := compstore.New()
		store.AddVector(component.Name, fake)
		processor := New(Options{Store: store})

		err := processor.Close(component)

		require.ErrorIs(t, err, assert.AnError)
		assert.True(t, fake.closeCalled)
		_, ok := store.GetVector(component.Name)
		assert.False(t, ok)
	})
}

func newVectorComponent() compapi.Component {
	return compapi.Component{
		ObjectMeta: metav1.ObjectMeta{Name: "mock"},
		Spec: compapi.ComponentSpec{
			Type:    "vector.mock",
			Version: "v1",
		},
	}
}
