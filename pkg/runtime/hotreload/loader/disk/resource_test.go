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

package disk

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	commonapi "github.com/dapr/dapr/pkg/apis/common"
	componentsapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	loaderdisk "github.com/dapr/dapr/pkg/internal/loader/disk"
	operatorpb "github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	"github.com/dapr/dapr/pkg/runtime/hotreload/loader"
	loadercompstore "github.com/dapr/dapr/pkg/runtime/hotreload/loader/store"
)

const (
	comp1 = `apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: comp1
spec:
  type: state.in-memory
  version: v1
`

	comp2 = `apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: comp2
spec:
  type: state.in-memory
  version: v1
`

	comp3 = `apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: comp3
spec:
  type: state.in-memory
  version: v1
`
)

func Test_Disk(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store := compstore.New()

	d, err := New(Options{
		Dirs:           []string{dir},
		ComponentStore: store,
	})
	require.NoError(t, err)

	errCh := make(chan error)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		errCh <- d.Run(ctx)
	}()

	t.Cleanup(func() {
		cancel()
		select {
		case err = <-errCh:
			require.NoError(t, err)
		case <-time.After(time.Second):
			assert.Fail(t, "expected to receive error")
		}
	})

	assert.Empty(t, store.ListComponents())

	ch, err := d.Components().Stream(context.Background())
	require.NoError(t, err)

	err = os.WriteFile(filepath.Join(dir, "f.yaml"), []byte(strings.Join([]string{comp1, comp2, comp3}, "\n---\n")), 0o600)
	require.NoError(t, err)

	var events []*loader.Event[componentsapi.Component]
	for i := 0; i < 3; i++ {
		select {
		case event := <-ch.EventCh:
			events = append(events, event)
		case <-time.After(time.Second * 3):
			assert.Fail(t, "expected to receive event")
		}
	}

	assert.ElementsMatch(t, []*loader.Event[componentsapi.Component]{
		{
			Type: operatorpb.ResourceEventType_CREATED,
			Resource: componentsapi.Component{
				ObjectMeta: metav1.ObjectMeta{Name: "comp1", Namespace: ""},
				TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "Component"},
				Spec:       componentsapi.ComponentSpec{Type: "state.in-memory", Version: "v1"},
			},
		},
		{
			Type: operatorpb.ResourceEventType_CREATED,
			Resource: componentsapi.Component{
				ObjectMeta: metav1.ObjectMeta{Name: "comp2", Namespace: ""},
				TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "Component"},
				Spec:       componentsapi.ComponentSpec{Type: "state.in-memory", Version: "v1"},
			},
		},
		{
			Type: operatorpb.ResourceEventType_CREATED,
			Resource: componentsapi.Component{
				ObjectMeta: metav1.ObjectMeta{Name: "comp3", Namespace: ""},
				TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "Component"},
				Spec:       componentsapi.ComponentSpec{Type: "state.in-memory", Version: "v1"},
			},
		},
	}, events)
}

func Test_Stream(t *testing.T) {
	t.Parallel()

	t.Run("if store empty and event happens, should send create event with all components", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		err := os.WriteFile(filepath.Join(dir, "f.yaml"), []byte(strings.Join([]string{comp1, comp2, comp3}, "\n---\n")), 0o600)
		require.NoError(t, err)

		store := compstore.New()

		updateCh := make(chan struct{})
		r := newResource[componentsapi.Component](
			loaderdisk.New[componentsapi.Component](dir),
			loadercompstore.NewComponent(store),
			updateCh,
		)

		errCh := make(chan error)
		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			errCh <- r.start(ctx)
		}()
		t.Cleanup(func() {
			cancel()
			require.NoError(t, <-errCh)
		})

		ch, err := r.Stream(context.Background())
		require.NoError(t, err)

		updateCh <- struct{}{}

		var events []*loader.Event[componentsapi.Component]
		for i := 0; i < 3; i++ {
			select {
			case event := <-ch.EventCh:
				events = append(events, event)
			case <-time.After(time.Second * 3):
				assert.Fail(t, "expected to receive event")
			}
		}

		assert.ElementsMatch(t, []*loader.Event[componentsapi.Component]{
			{
				Type: operatorpb.ResourceEventType_CREATED,
				Resource: componentsapi.Component{
					ObjectMeta: metav1.ObjectMeta{Name: "comp1"},
					TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "Component"},
					Spec:       componentsapi.ComponentSpec{Type: "state.in-memory", Version: "v1"},
				},
			},
			{
				Type: operatorpb.ResourceEventType_CREATED,
				Resource: componentsapi.Component{
					ObjectMeta: metav1.ObjectMeta{Name: "comp2"},
					TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "Component"},
					Spec:       componentsapi.ComponentSpec{Type: "state.in-memory", Version: "v1"},
				},
			},
			{
				Type: operatorpb.ResourceEventType_CREATED,
				Resource: componentsapi.Component{
					ObjectMeta: metav1.ObjectMeta{Name: "comp3"},
					TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "Component"},
					Spec:       componentsapi.ComponentSpec{Type: "state.in-memory", Version: "v1"},
				},
			},
		}, events)
	})

	t.Run("if store has a component and event happens, should send create event with new components", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		err := os.WriteFile(filepath.Join(dir, "f.yaml"), []byte(strings.Join([]string{comp1, comp2, comp3}, "\n---\n")), 0o600)
		require.NoError(t, err)

		store := compstore.New()
		require.NoError(t, store.AddPendingComponentForCommit(componentsapi.Component{
			ObjectMeta: metav1.ObjectMeta{Name: "comp1"},
			TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "Component"},
			Spec:       componentsapi.ComponentSpec{Type: "state.in-memory", Version: "v1"},
		}))
		require.NoError(t, store.CommitPendingComponent())

		updateCh := make(chan struct{})
		r := newResource[componentsapi.Component](
			loaderdisk.New[componentsapi.Component](dir),
			loadercompstore.NewComponent(store),
			updateCh,
		)

		errCh := make(chan error)
		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			errCh <- r.start(ctx)
		}()
		t.Cleanup(func() {
			cancel()
			require.NoError(t, <-errCh)
		})

		ch, err := r.Stream(context.Background())
		require.NoError(t, err)

		updateCh <- struct{}{}

		var events []*loader.Event[componentsapi.Component]
		for i := 0; i < 2; i++ {
			select {
			case event := <-ch.EventCh:
				events = append(events, event)
			case <-time.After(time.Second * 3):
				assert.Fail(t, "expected to receive event")
			}
		}

		assert.ElementsMatch(t, []*loader.Event[componentsapi.Component]{
			{
				Type: operatorpb.ResourceEventType_CREATED,
				Resource: componentsapi.Component{
					ObjectMeta: metav1.ObjectMeta{Name: "comp2"},
					TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "Component"},
					Spec:       componentsapi.ComponentSpec{Type: "state.in-memory", Version: "v1"},
				},
			},
			{
				Type: operatorpb.ResourceEventType_CREATED,
				Resource: componentsapi.Component{
					ObjectMeta: metav1.ObjectMeta{Name: "comp3"},
					TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "Component"},
					Spec:       componentsapi.ComponentSpec{Type: "state.in-memory", Version: "v1"},
				},
			},
		}, events)
	})

	t.Run("if store has a component and event happens, should send create/update/delete events components", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		err := os.WriteFile(filepath.Join(dir, "f.yaml"), []byte(strings.Join([]string{comp2, comp3}, "\n---\n")), 0o600)
		require.NoError(t, err)

		store := compstore.New()
		require.NoError(t, store.AddPendingComponentForCommit(componentsapi.Component{
			ObjectMeta: metav1.ObjectMeta{Name: "comp1"},
			TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "Component"},
			Spec:       componentsapi.ComponentSpec{Type: "state.in-memory", Version: "v1"},
		}))
		require.NoError(t, store.CommitPendingComponent())
		require.NoError(t, store.AddPendingComponentForCommit(componentsapi.Component{
			ObjectMeta: metav1.ObjectMeta{Name: "comp2"},
			TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "Component"},
			Spec: componentsapi.ComponentSpec{
				Type: "state.in-memory", Version: "v1",
				Metadata: []commonapi.NameValuePair{{Name: "foo", EnvRef: "bar"}},
			},
		}))
		require.NoError(t, store.CommitPendingComponent())

		updateCh := make(chan struct{})
		r := newResource[componentsapi.Component](
			loaderdisk.New[componentsapi.Component](dir),
			loadercompstore.NewComponent(store),
			updateCh,
		)

		errCh := make(chan error)
		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			errCh <- r.start(ctx)
		}()
		t.Cleanup(func() {
			cancel()
			require.NoError(t, <-errCh)
		})

		ch, err := r.Stream(context.Background())
		require.NoError(t, err)

		updateCh <- struct{}{}

		var events []*loader.Event[componentsapi.Component]
		for i := 0; i < 3; i++ {
			select {
			case event := <-ch.EventCh:
				events = append(events, event)
			case <-time.After(time.Second * 5):
				assert.Fail(t, "expected to receive event")
			}
		}

		assert.ElementsMatch(t, []*loader.Event[componentsapi.Component]{
			{
				Type: operatorpb.ResourceEventType_DELETED,
				Resource: componentsapi.Component{
					ObjectMeta: metav1.ObjectMeta{Name: "comp1"},
					TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "Component"},
					Spec:       componentsapi.ComponentSpec{Type: "state.in-memory", Version: "v1"},
				},
			},
			{
				Type: operatorpb.ResourceEventType_UPDATED,
				Resource: componentsapi.Component{
					ObjectMeta: metav1.ObjectMeta{Name: "comp2"},
					TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "Component"},
					Spec:       componentsapi.ComponentSpec{Type: "state.in-memory", Version: "v1"},
				},
			},
			{
				Type: operatorpb.ResourceEventType_CREATED,
				Resource: componentsapi.Component{
					ObjectMeta: metav1.ObjectMeta{Name: "comp3"},
					TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "Component"},
					Spec:       componentsapi.ComponentSpec{Type: "state.in-memory", Version: "v1"},
				},
			},
		}, events)
	})
}
