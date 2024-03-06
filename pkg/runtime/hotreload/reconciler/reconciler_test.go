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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clocktesting "k8s.io/utils/clock/testing"

	componentsapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	"github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	"github.com/dapr/dapr/pkg/runtime/hotreload/differ"
	"github.com/dapr/dapr/pkg/runtime/hotreload/loader"
	"github.com/dapr/dapr/pkg/runtime/hotreload/loader/fake"
)

func Test_Run(t *testing.T) {
	t.Run("should reconcile when ticker reaches 60 seconds", func(t *testing.T) {
		compLoader := fake.NewFake[componentsapi.Component]()
		loader := fake.New().WithComponent(compLoader)

		var listCalled atomic.Int32
		compLoader.WithList(func(context.Context) (*differ.LocalRemoteResources[componentsapi.Component], error) {
			listCalled.Add(1)
			return nil, nil
		})

		r := NewComponent(Options[componentsapi.Component]{
			Loader:    loader,
			CompStore: compstore.New(),
		})
		fakeClock := clocktesting.NewFakeClock(time.Now())
		r.clock = fakeClock

		errCh := make(chan error)
		go func() {
			errCh <- r.Run(context.Background())
		}()

		assert.Eventually(t, fakeClock.HasWaiters, time.Second*3, time.Millisecond*100)

		assert.Equal(t, int32(0), listCalled.Load())

		fakeClock.Step(time.Second * 60)

		assert.Eventually(t, func() bool {
			return listCalled.Load() == 1
		}, time.Second*3, time.Millisecond*100)

		require.NoError(t, r.Close())

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
		compLoader.WithStream(func(context.Context) (<-chan *loader.Event[componentsapi.Component], error) {
			return compCh, nil
		})

		r := NewComponent(Options[componentsapi.Component]{
			Loader:    fake.New().WithComponent(compLoader),
			CompStore: compstore.New(),
		})
		fakeClock := clocktesting.NewFakeClock(time.Now())
		r.clock = fakeClock

		mngr := newFakeManager()
		mngr.Loader = compLoader
		updateCh := make(chan componentsapi.Component)
		deleteCh := make(chan componentsapi.Component)
		mngr.deleteFn = func(c componentsapi.Component) {
			deleteCh <- c
		}
		mngr.updateFn = func(_ context.Context, c componentsapi.Component) {
			updateCh <- c
		}

		r.manager = mngr

		errCh := make(chan error)
		go func() {
			errCh <- r.Run(context.Background())
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

		require.NoError(t, r.Close())
		require.NoError(t, <-errCh)
	})
}

func Test_reconcile(t *testing.T) {
	const caseNum = 100

	deleted := make([]componentsapi.Component, caseNum)
	updated := make([]componentsapi.Component, caseNum)
	created := make([]componentsapi.Component, caseNum)

	fz := fuzz.New()
	for i := 0; i < caseNum; i++ {
		fz.Fuzz(&deleted[i])
		fz.Fuzz(&updated[i])
		fz.Fuzz(&created[i])
	}

	eventCh := make(chan componentsapi.Component)
	mngr := newFakeManager()
	mngr.deleteFn = func(c componentsapi.Component) {
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
			r.reconcile(context.Background(), &differ.Result[componentsapi.Component]{
				Deleted: deleted,
				Updated: updated,
				Created: created,
			})
		}()

		var got []componentsapi.Component
		for i := 0; i < caseNum; i++ {
			select {
			case e := <-eventCh:
				got = append(got, e)
			case <-time.After(time.Second * 3):
				t.Error("did not get event in time")
			}
		}
		assert.ElementsMatch(t, deleted, got)

		got = []componentsapi.Component{}
		for i := 0; i < caseNum; i++ {
			select {
			case e := <-eventCh:
				got = append(got, e)
			case <-time.After(time.Second * 3):
				t.Error("did not get event in time")
			}
		}
		assert.ElementsMatch(t, updated, got)

		got = []componentsapi.Component{}
		for i := 0; i < caseNum; i++ {
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

	mngr.deleteFn = func(c componentsapi.Component) {
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

	r.handleEvent(context.Background(), &loader.Event[componentsapi.Component]{
		Type:     operator.ResourceEventType_CREATED,
		Resource: comp1,
	})
	assert.Equal(t, 1, updateCalled)
	assert.Equal(t, 0, deleteCalled)

	r.handleEvent(context.Background(), &loader.Event[componentsapi.Component]{
		Type:     operator.ResourceEventType_UPDATED,
		Resource: comp1,
	})
	assert.Equal(t, 2, updateCalled)
	assert.Equal(t, 0, deleteCalled)

	r.handleEvent(context.Background(), &loader.Event[componentsapi.Component]{
		Type:     operator.ResourceEventType_DELETED,
		Resource: comp1,
	})
	assert.Equal(t, 2, updateCalled)
	assert.Equal(t, 1, deleteCalled)
}

type fakeManager struct {
	loader.Loader[componentsapi.Component]
	updateFn func(context.Context, componentsapi.Component)
	deleteFn func(componentsapi.Component)
}

func newFakeManager() *fakeManager {
	return &fakeManager{
		updateFn: func(context.Context, componentsapi.Component) {},
		deleteFn: func(componentsapi.Component) {},
	}
}

//nolint:unused
func (f *fakeManager) update(ctx context.Context, comp componentsapi.Component) {
	f.updateFn(ctx, comp)
}

//nolint:unused
func (f *fakeManager) delete(comp componentsapi.Component) {
	f.deleteFn(comp)
}
