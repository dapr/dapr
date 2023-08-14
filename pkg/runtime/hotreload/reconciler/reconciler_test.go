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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clocktesting "k8s.io/utils/clock/testing"

	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	"github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	"github.com/dapr/dapr/pkg/runtime/hotreload/differ"
	"github.com/dapr/dapr/pkg/runtime/hotreload/loader"
	"github.com/dapr/dapr/pkg/runtime/hotreload/loader/fake"
)

func Test_Run(t *testing.T) {
	t.Run("should reconcile when ticker reaches 60 seconds", func(t *testing.T) {
		compLoader := fake.NewFake[compapi.Component]()
		loader := fake.New().WithComponent(compLoader)

		var listCalled atomic.Int32
		compLoader.WithList(func(context.Context) (*differ.LocalRemoteResources[compapi.Component], error) {
			listCalled.Add(1)
			return nil, nil
		})

		r := NewComponent(Options[compapi.Component]{
			Loader:    loader,
			CompStore: compstore.New(),
		}, nil)
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

		assert.NoError(t, r.Close())

		select {
		case err := <-errCh:
			assert.NoError(t, err)
		case <-time.After(time.Second * 3):
			t.Error("reconciler did not return in time")
		}
	})

	t.Run("should reconcile when ticker reaches 60 seconds", func(t *testing.T) {
		compLoader := fake.NewFake[compapi.Component]()

		compCh := make(chan *loader.Event[compapi.Component])
		compLoader.WithStream(func(context.Context) (<-chan *loader.Event[compapi.Component], error) {
			return compCh, nil
		})

		r := NewComponent(Options[compapi.Component]{
			Loader:    fake.New().WithComponent(compLoader),
			CompStore: compstore.New(),
		}, nil)
		fakeClock := clocktesting.NewFakeClock(time.Now())
		r.clock = fakeClock

		mngr := newFakeManager()
		mngr.Loader = compLoader
		updateCh := make(chan compapi.Component)
		deleteCh := make(chan compapi.Component)
		mngr.deleteFn = func(c compapi.Component) {
			deleteCh <- c
		}
		mngr.updateFn = func(_ context.Context, c compapi.Component) {
			updateCh <- c
		}

		r.manager = mngr

		errCh := make(chan error)
		go func() {
			errCh <- r.Run(context.Background())
		}()

		comp1 := compapi.Component{ObjectMeta: metav1.ObjectMeta{Name: "foo"}}

		select {
		case compCh <- &loader.Event[compapi.Component]{
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
		case compCh <- &loader.Event[compapi.Component]{
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
		case compCh <- &loader.Event[compapi.Component]{
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

		assert.NoError(t, r.Close())
		assert.NoError(t, <-errCh)
	})
}

func Test_reconcile(t *testing.T) {
	const caseNum = 100

	deleted := make([]compapi.Component, caseNum)
	updated := make([]compapi.Component, caseNum)
	created := make([]compapi.Component, caseNum)

	fz := fuzz.New()
	for i := 0; i < caseNum; i++ {
		fz.Fuzz(&deleted[i])
		fz.Fuzz(&updated[i])
		fz.Fuzz(&created[i])
	}

	eventCh := make(chan compapi.Component)
	mngr := newFakeManager()
	mngr.deleteFn = func(c compapi.Component) {
		eventCh <- c
	}
	mngr.updateFn = func(_ context.Context, c compapi.Component) {
		eventCh <- c
	}

	r := &Reconciler[compapi.Component]{
		manager: mngr,
	}

	t.Run("events should be sent in the correct grouped order", func(t *testing.T) {
		errCh := make(chan error)
		go func() {
			errCh <- r.reconcile(context.Background(), &differ.Result[compapi.Component]{
				Deleted: deleted,
				Updated: updated,
				Created: created,
			})
		}()

		var got []compapi.Component
		for i := 0; i < caseNum; i++ {
			select {
			case e := <-eventCh:
				got = append(got, e)
			case <-time.After(time.Second * 3):
				t.Error("did not get event in time")
			}
		}
		assert.ElementsMatch(t, deleted, got)

		got = []compapi.Component{}
		for i := 0; i < caseNum; i++ {
			select {
			case e := <-eventCh:
				got = append(got, e)
			case <-time.After(time.Second * 3):
				t.Error("did not get event in time")
			}
		}
		assert.ElementsMatch(t, updated, got)

		got = []compapi.Component{}
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
		case err := <-errCh:
			assert.NoError(t, err)
		case <-time.After(time.Second * 3):
			t.Error("did not get reconcile return in time")
		}
	})
}

func Test_handleEvent(t *testing.T) {
	mngr := newFakeManager()

	updateCalled, deleteCalled := 0, 0
	comp1 := compapi.Component{ObjectMeta: metav1.ObjectMeta{Name: "foo"}}

	mngr.deleteFn = func(c compapi.Component) {
		assert.Equal(t, comp1, c)
		deleteCalled++
	}
	mngr.updateFn = func(_ context.Context, c compapi.Component) {
		assert.Equal(t, comp1, c)
		updateCalled++
	}

	r := &Reconciler[compapi.Component]{manager: mngr}

	assert.Equal(t, 0, updateCalled)
	assert.Equal(t, 0, deleteCalled)

	r.handleEvent(context.Background(), &loader.Event[compapi.Component]{
		Type:     operator.ResourceEventType_CREATED,
		Resource: comp1,
	})
	assert.Equal(t, 1, updateCalled)
	assert.Equal(t, 0, deleteCalled)

	r.handleEvent(context.Background(), &loader.Event[compapi.Component]{
		Type:     operator.ResourceEventType_UPDATED,
		Resource: comp1,
	})
	assert.Equal(t, 2, updateCalled)
	assert.Equal(t, 0, deleteCalled)

	r.handleEvent(context.Background(), &loader.Event[compapi.Component]{
		Type:     operator.ResourceEventType_DELETED,
		Resource: comp1,
	})
	assert.Equal(t, 2, updateCalled)
	assert.Equal(t, 1, deleteCalled)
}

type fakeManager struct {
	loader.Loader[compapi.Component]
	updateFn func(context.Context, compapi.Component)
	deleteFn func(compapi.Component)
}

func newFakeManager() *fakeManager {
	return &fakeManager{
		updateFn: func(context.Context, compapi.Component) {
		},
		deleteFn: func(compapi.Component) {
		},
	}
}

//nolint:unused
func (f *fakeManager) update(ctx context.Context, comp compapi.Component) {
	f.updateFn(ctx, comp)
}

//nolint:unused
func (f *fakeManager) delete(comp compapi.Component) {
	f.deleteFn(comp)
}
