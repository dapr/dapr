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

package reconciler

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	fuzz "github.com/google/gofuzz"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clocktesting "k8s.io/utils/clock/testing"

	"github.com/dapr/dapr/pkg/apis/common"
	componentsapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	"github.com/dapr/dapr/pkg/healthz"
	"github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	"github.com/dapr/dapr/pkg/runtime/hotreload/differ"
	"github.com/dapr/dapr/pkg/runtime/hotreload/loader"
	"github.com/dapr/dapr/pkg/runtime/hotreload/loader/fake"
	"github.com/dapr/dapr/pkg/runtime/meta"
	"github.com/dapr/dapr/pkg/runtime/processor"
)

func Test_Run(t *testing.T) {
	t.Run("should reconcile when ticker reaches 60 seconds", func(t *testing.T) {
		compLoader := fake.NewFake[componentsapi.Component]()
		loader := fake.New().WithComponents(compLoader)

		var listCalled atomic.Int32

		compLoader.WithList(func(context.Context) (*differ.LocalRemoteResources[componentsapi.Component], error) {
			listCalled.Add(1)
			return nil, nil
		})

		r, _ := NewComponents(Options[componentsapi.Component]{
			Loader:    loader,
			CompStore: compstore.New(),
			Healthz:   healthz.New(),
		})
		fakeClock := clocktesting.NewFakeClock(time.Now())
		r.clock = fakeClock

		errCh := make(chan error)
		ctx, cancel := context.WithCancel(t.Context())

		go func() {
			errCh <- r.Run(ctx)
		}()

		assert.Eventually(t, fakeClock.HasWaiters, time.Second*3, time.Millisecond*100)

		assert.Equal(t, int32(0), listCalled.Load())

		fakeClock.Step(time.Second * 60)

		assert.Eventually(t, func() bool {
			return listCalled.Load() == 1
		}, time.Second*3, time.Millisecond*100)

		cancel()

		select {
		case err := <-errCh:
			require.NoError(t, err)
		case <-time.After(time.Second * 3):
			t.Error("reconciler did not return in time")
		}
	})

	t.Run("should reconcile when ticker reaches 60 seconds", func(t *testing.T) {
		compLoader := fake.NewFake[componentsapi.Component]()

		compCh := make(chan *loader.Event[componentsapi.Component])

		compLoader.WithStream(func(context.Context) (*loader.StreamConn[componentsapi.Component], error) {
			return &loader.StreamConn[componentsapi.Component]{
				EventCh:     compCh,
				ReconcileCh: make(chan struct{}),
			}, nil
		})

		r, _ := NewComponents(Options[componentsapi.Component]{
			Loader:    fake.New().WithComponents(compLoader),
			CompStore: compstore.New(),
			Healthz:   healthz.New(),
		})
		fakeClock := clocktesting.NewFakeClock(time.Now())
		r.clock = fakeClock

		mngr := newFakeManager()
		mngr.Loader = compLoader
		updateCh := make(chan componentsapi.Component)
		deleteCh := make(chan componentsapi.Component)
		mngr.deleteFn = func(_ context.Context, c componentsapi.Component) {
			deleteCh <- c
		}
		mngr.updateFn = func(_ context.Context, c componentsapi.Component) {
			updateCh <- c
		}

		r.manager = mngr

		errCh := make(chan error)
		ctx, cancel := context.WithCancel(t.Context())

		go func() {
			errCh <- r.Run(ctx)
		}()

		comp1 := componentsapi.Component{ObjectMeta: metav1.ObjectMeta{Name: "foo"}}

		select {
		case compCh <- &loader.Event[componentsapi.Component]{
			Type:     operator.ResourceEventType_CREATED,
			Resource: comp1,
		}:
		case <-time.After(time.Second * 3):
			t.Error("reconciler did not return in time")
		}

		select {
		case e := <-updateCh:
			assert.Equal(t, comp1, e)
		case <-time.After(time.Second * 3):
			t.Error("did not get event in time")
		}

		select {
		case compCh <- &loader.Event[componentsapi.Component]{
			Type:     operator.ResourceEventType_UPDATED,
			Resource: comp1,
		}:
		case <-time.After(time.Second * 3):
			t.Error("reconciler did not return in time")
		}

		select {
		case e := <-updateCh:
			assert.Equal(t, comp1, e)
		case <-time.After(time.Second * 3):
			t.Error("did not get event in time")
		}

		select {
		case compCh <- &loader.Event[componentsapi.Component]{
			Type:     operator.ResourceEventType_DELETED,
			Resource: comp1,
		}:
		case <-time.After(time.Second * 3):
			t.Error("reconciler did not return in time")
		}

		select {
		case e := <-deleteCh:
			assert.Equal(t, comp1, e)
		case <-time.After(time.Second * 3):
			t.Error("did not get event in time")
		}

		cancel()

		select {
		case err := <-errCh:
			require.NoError(t, err)
		case <-time.After(time.Second * 3):
			t.Error("reconciler did not return in time")
		}
	})

	t.Run("should reconcile when receive event from reconcile channel", func(t *testing.T) {
		compLoader := fake.NewFake[componentsapi.Component]()

		recCh := make(chan struct{})

		compLoader.WithStream(func(context.Context) (*loader.StreamConn[componentsapi.Component], error) {
			return &loader.StreamConn[componentsapi.Component]{
				EventCh:     make(chan *loader.Event[componentsapi.Component]),
				ReconcileCh: recCh,
			}, nil
		})

		r, _ := NewComponents(Options[componentsapi.Component]{
			Loader:    fake.New().WithComponents(compLoader),
			CompStore: compstore.New(),
			Healthz:   healthz.New(),
		})
		fakeClock := clocktesting.NewFakeClock(time.Now())
		r.clock = fakeClock

		mngr := newFakeManager()
		mngr.Loader = compLoader
		updateCh := make(chan componentsapi.Component)
		deleteCh := make(chan componentsapi.Component)
		mngr.deleteFn = func(_ context.Context, c componentsapi.Component) {
			deleteCh <- c
		}
		mngr.updateFn = func(_ context.Context, c componentsapi.Component) {
			updateCh <- c
		}

		r.manager = mngr

		errCh := make(chan error)
		ctx, cancel := context.WithCancel(t.Context())

		go func() {
			errCh <- r.Run(ctx)
		}()

		comp1 := componentsapi.Component{ObjectMeta: metav1.ObjectMeta{Name: "foo"}}

		compLoader.WithList(func(context.Context) (*differ.LocalRemoteResources[componentsapi.Component], error) {
			return &differ.LocalRemoteResources[componentsapi.Component]{
				Local:  []componentsapi.Component{},
				Remote: []componentsapi.Component{comp1},
			}, nil
		})

		select {
		case recCh <- struct{}{}:
		case <-time.After(time.Second * 3):
			t.Error("reconciler did not return in time")
		}

		select {
		case e := <-updateCh:
			assert.Equal(t, comp1, e)
		case <-time.After(time.Second * 3):
			t.Error("did not get event in time")
		}

		comp2 := comp1.DeepCopy()
		comp2.Spec = componentsapi.ComponentSpec{Version: "123"}

		compLoader.WithList(func(context.Context) (*differ.LocalRemoteResources[componentsapi.Component], error) {
			return &differ.LocalRemoteResources[componentsapi.Component]{
				Local:  []componentsapi.Component{comp1},
				Remote: []componentsapi.Component{*comp2},
			}, nil
		})

		select {
		case recCh <- struct{}{}:
		case <-time.After(time.Second * 3):
			t.Error("reconciler did not return in time")
		}

		select {
		case e := <-updateCh:
			assert.Equal(t, *comp2, e)
		case <-time.After(time.Second * 3):
			t.Error("did not get event in time")
		}

		compLoader.WithList(func(context.Context) (*differ.LocalRemoteResources[componentsapi.Component], error) {
			return &differ.LocalRemoteResources[componentsapi.Component]{
				Local:  []componentsapi.Component{*comp2},
				Remote: []componentsapi.Component{},
			}, nil
		})

		select {
		case recCh <- struct{}{}:
		case <-time.After(time.Second * 3):
			t.Error("reconciler did not return in time")
		}

		select {
		case e := <-deleteCh:
			assert.Equal(t, *comp2, e)
		case <-time.After(time.Second * 3):
			t.Error("did not get event in time")
		}

		cancel()

		select {
		case err := <-errCh:
			require.NoError(t, err)
		case <-time.After(time.Second * 3):
			t.Error("reconciler did not return in time")
		}
	})
}

func Test_reconcile(t *testing.T) {
	const caseNum = 100

	deleted := make([]componentsapi.Component, caseNum)
	updated := make([]componentsapi.Component, caseNum)
	created := make([]componentsapi.Component, caseNum)

	fz := fuzz.New()
	for i := range caseNum {
		fz.Fuzz(&deleted[i])
		fz.Fuzz(&updated[i])
		fz.Fuzz(&created[i])
	}

	eventCh := make(chan componentsapi.Component)
	mngr := newFakeManager()
	mngr.deleteFn = func(_ context.Context, c componentsapi.Component) {
		eventCh <- c
	}
	mngr.updateFn = func(_ context.Context, c componentsapi.Component) {
		eventCh <- c
	}

	r := &Reconciler[componentsapi.Component]{
		manager: mngr,
	}

	t.Run("events should be sent in the correct grouped order", func(t *testing.T) {
		recDone := make(chan struct{})

		go func() {
			defer close(recDone)

			r.reconcile(t.Context(), &differ.Result[componentsapi.Component]{
				Deleted: deleted,
				Updated: updated,
				Created: created,
			})
		}()

		var got []componentsapi.Component

		for range caseNum {
			select {
			case e := <-eventCh:
				got = append(got, e)
			case <-time.After(time.Second * 3):
				t.Error("did not get event in time")
			}
		}

		assert.ElementsMatch(t, deleted, got)

		got = []componentsapi.Component{}

		for range caseNum {
			select {
			case e := <-eventCh:
				got = append(got, e)
			case <-time.After(time.Second * 3):
				t.Error("did not get event in time")
			}
		}

		assert.ElementsMatch(t, updated, got)

		got = []componentsapi.Component{}

		for range caseNum {
			select {
			case e := <-eventCh:
				got = append(got, e)
			case <-time.After(time.Second * 3):
				t.Error("did not get event in time")
			}
		}

		assert.ElementsMatch(t, created, got)

		select {
		case <-recDone:
		case <-time.After(time.Second * 3):
			t.Error("did not get reconcile return in time")
		}
	})
}

func Test_handleEvent(t *testing.T) {
	mngr := newFakeManager()

	updateCalled, deleteCalled := 0, 0
	comp1 := componentsapi.Component{ObjectMeta: metav1.ObjectMeta{Name: "foo"}}

	mngr.deleteFn = func(_ context.Context, c componentsapi.Component) {
		assert.Equal(t, comp1, c)

		deleteCalled++
	}
	mngr.updateFn = func(_ context.Context, c componentsapi.Component) {
		assert.Equal(t, comp1, c)

		updateCalled++
	}

	r := &Reconciler[componentsapi.Component]{manager: mngr}

	assert.Equal(t, 0, updateCalled)
	assert.Equal(t, 0, deleteCalled)

	r.handleEvent(t.Context(), &loader.Event[componentsapi.Component]{
		Type:     operator.ResourceEventType_CREATED,
		Resource: comp1,
	})
	assert.Equal(t, 1, updateCalled)
	assert.Equal(t, 0, deleteCalled)

	r.handleEvent(t.Context(), &loader.Event[componentsapi.Component]{
		Type:     operator.ResourceEventType_UPDATED,
		Resource: comp1,
	})
	assert.Equal(t, 2, updateCalled)
	assert.Equal(t, 0, deleteCalled)

	r.handleEvent(t.Context(), &loader.Event[componentsapi.Component]{
		Type:     operator.ResourceEventType_DELETED,
		Resource: comp1,
	})
	assert.Equal(t, 2, updateCalled)
	assert.Equal(t, 1, deleteCalled)
}

type fakeManager struct {
	loader.Loader[componentsapi.Component]

	updateFn func(context.Context, componentsapi.Component)
	deleteFn func(context.Context, componentsapi.Component)
}

func newFakeManager() *fakeManager {
	return &fakeManager{
		updateFn: func(context.Context, componentsapi.Component) {},
		deleteFn: func(context.Context, componentsapi.Component) {},
	}
}

//nolint:unused
func (f *fakeManager) update(ctx context.Context, comp componentsapi.Component) {
	f.updateFn(ctx, comp)
}

//nolint:unused
func (f *fakeManager) delete(ctx context.Context, comp componentsapi.Component) {
	f.deleteFn(ctx, comp)
}

func Test_Secrets(t *testing.T) {
	t.Run("should trigger hot reload when secrets change", func(t *testing.T) {
		compStore := compstore.New()
		mngr := newFakeManager()
		updateCh := make(chan componentsapi.Component, 1)
		mngr.updateFn = func(_ context.Context, c componentsapi.Component) {
			updateCh <- c
		}

		// Mock SecretManager
		secMngr := &mockSecretManager{}

		fakeClock := clocktesting.NewFakeClock(time.Now())
		s := &Secrets{
			compStore:     compStore,
			secretManager: secMngr,
			manager:       mngr,
			clock:         fakeClock,
		}

		comp1 := componentsapi.Component{
			ObjectMeta: metav1.ObjectMeta{Name: "foo"},
			Spec: componentsapi.ComponentSpec{
				Metadata: []common.NameValuePair{
					{Name: "user", SecretKeyRef: common.SecretKeyRef{Name: "mysecret", Key: "user"}},
				},
			},
		}
		compStore.AddPendingComponentForCommit(comp1)
		compStore.CommitPendingComponent()

		// Prepare SecretManager to respond with updated component
		secMngr.processResourceFn = func(ctx context.Context, res meta.Resource) (bool, string) {
			if _, ok := res.(*componentsapi.Component); ok {
				// No change yet
				return false, ""
			}
			return false, ""
		}

		errCh := make(chan error)
		ctx, cancel := context.WithCancel(t.Context())
		go func() {
			errCh <- s.Run(ctx)
		}()

		assert.Eventually(t, fakeClock.HasWaiters, time.Second, time.Millisecond*50)

		// Tick!
		fakeClock.Step(time.Minute)

		// Nothing should happen
		select {
		case <-updateCh:
			t.Fatal("should not have triggered update")
		case <-time.After(time.Millisecond * 100):
		}

		// Set mock to return updated component
		secMngr.processResourceFn = func(ctx context.Context, res meta.Resource) (bool, string) {
			if c, ok := res.(*componentsapi.Component); ok {
				c.Spec.Metadata[0].Value = common.DynamicValue{JSON: apiextensionsv1.JSON{Raw: []byte("\"updated-user\"")}}
				return true, ""
			}
			return true, ""
		}

		// Tick again!
		fakeClock.Step(time.Minute)

		// Verify update was triggered!
		select {
		case e := <-updateCh:
			assert.Equal(t, "foo", e.Name)
			assert.Equal(t, "\"updated-user\"", string(e.Spec.Metadata[0].Value.JSON.Raw))
		case <-time.After(time.Second * 3):
			t.Fatal("did not get update in time")
		}

		cancel()
		<-errCh
	})
}

type mockSecretManager struct {
	processor.SecretManager
	processResourceFn func(context.Context, meta.Resource) (bool, string)
}

func (m *mockSecretManager) ProcessResource(ctx context.Context, res meta.Resource) (bool, string) {
	return m.processResourceFn(ctx, res)
}

func (m *mockSecretManager) Init(ctx context.Context, comp componentsapi.Component) error { return nil }
func (m *mockSecretManager) Close(comp componentsapi.Component) error                     { return nil }
