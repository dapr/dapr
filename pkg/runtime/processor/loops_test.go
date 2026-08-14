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

package processor

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/dapr/components-contrib/pubsub"
	"github.com/dapr/components-contrib/secretstores"
	contribstate "github.com/dapr/components-contrib/state"
	commonapi "github.com/dapr/dapr/pkg/apis/common"
	componentsapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	rtmock "github.com/dapr/dapr/pkg/runtime/mock"
	daprt "github.com/dapr/dapr/pkg/testing"
	"github.com/dapr/kit/logger"
)

// TestProcessorFlushBarrier drives several component inits through the full
// root -> category -> instance loop hierarchy and asserts that Flush only
// returns once every in-flight init has committed. This covers the root loop's
// routing and in-flight/Barrier accounting.
func TestProcessorFlushBarrier(t *testing.T) {
	proc, reg := newTestProc()
	startProc(t, proc)

	mockPubSub := new(daprt.MockPubSub)
	reg.PubSubs().RegisterComponent(
		func(logger.Logger) pubsub.PubSub { return mockPubSub },
		"mockPubSub",
	)
	mockPubSub.On("Init", mock.Anything).Return(nil)

	names := []string{"ps1", "ps2", "ps3"}
	for _, n := range names {
		require.NotNil(t, proc.AddPendingComponent(t.Context(), inlinePubsubComp(n)))
	}

	require.NoError(t, proc.Flush(t.Context()))

	for _, n := range names {
		_, ok := proc.compStore.GetComponent(n)
		assert.True(t, ok, "component %s must be committed once Flush returns", n)
	}
}

// TestProcessorSecretStoreDependentReenqueue covers the root loop's deferred
// dependent handling: a component that references a secret store which has not
// been loaded yet is parked, and then processed automatically once that secret
// store initialises. This exercises pendingDependents, the InstanceInitDone
// re-enqueue, and the pre-counted in-flight accounting that keeps Flush honest.
func TestProcessorSecretStoreDependentReenqueue(t *testing.T) {
	proc, reg := newTestProc()
	startProc(t, proc)

	mockPubSub := new(daprt.MockPubSub)
	reg.PubSubs().RegisterComponent(
		func(logger.Logger) pubsub.PubSub { return mockPubSub },
		"mockPubSub",
	)
	mockPubSub.On("Init", mock.Anything).Return(nil)
	reg.SecretStores().RegisterComponent(
		func(logger.Logger) secretstores.SecretStore { return rtmock.NewMockKubernetesStore() },
		"mockSecret",
	)

	// Submit a pubsub component that depends on the not-yet-loaded "mystore"
	// secret store. It is deferred: success is reported immediately and nothing
	// is committed yet.
	dependent := inlinePubsubComp("needs-secret")
	dependent.SecretStore = "mystore"
	dependent.Spec.Metadata = append(dependent.Spec.Metadata, commonapi.NameValuePair{
		Name:         "password",
		SecretKeyRef: commonapi.SecretKeyRef{Name: "mysecret", Key: "key1"},
	})

	ch := proc.AddPendingComponent(t.Context(), dependent)
	require.NotNil(t, ch)
	require.NoError(t, <-ch)
	_, ok := proc.compStore.GetComponent("needs-secret")
	require.False(t, ok, "dependent must not be committed while its secret store is unready")

	// Load the secret store the component depends on. Its completion flushes the
	// parked dependent back through the root loop.
	require.NoError(t, proc.Init(t.Context(), componentsapi.Component{
		ObjectMeta: metav1.ObjectMeta{Name: "mystore"},
		Spec:       componentsapi.ComponentSpec{Type: "secretstores.mockSecret", Version: "v1"},
	}))

	require.NoError(t, proc.Flush(t.Context()))

	_, ok = proc.compStore.GetComponent("needs-secret")
	assert.True(t, ok, "dependent must be processed once its secret store initialises")
}

func actorStateStoreComp(name string) componentsapi.Component {
	return componentsapi.Component{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: componentsapi.ComponentSpec{
			Type:    "state.mockState",
			Version: "v1",
			Metadata: []commonapi.NameValuePair{{
				Name: "actorStateStore",
				Value: commonapi.DynamicValue{
					JSON: apiextv1.JSON{Raw: []byte(`"true"`)},
				},
			}},
		},
	}
}

// TestProcessorActorStateStoreInitRetry asserts that a transiently failing
// actor state store init is retried with backoff instead of being recorded
// as a fatal runtime error: the component commits once its backing store
// recovers and the processor loop stays alive throughout (chaos campaign
// 10/08/2026 S6: a 25s state store outage crash-looped every sidecar).
func TestProcessorActorStateStoreInitRetry(t *testing.T) {
	proc, reg := newTestProc()
	startProc(t, proc)

	mockStore := new(daprt.MockStateStore)
	reg.StateStores().RegisterComponent(
		func(logger.Logger) contribstate.Store { return mockStore },
		"mockState",
	)
	mockStore.On("Init", mock.Anything, mock.Anything).Return(errors.New("connection refused")).Twice()
	mockStore.On("Init", mock.Anything, mock.Anything).Return(nil)
	mockStore.On("Features").Return(nil)

	ch := proc.AddPendingComponent(t.Context(), actorStateStoreComp("mystore"))
	require.NotNil(t, ch)

	select {
	case err := <-ch:
		require.NoError(t, err, "init must succeed after the store recovers")
	case <-time.After(30 * time.Second):
		t.Fatal("timed out waiting for the actor state store init to recover")
	}

	_, ok := proc.compStore.GetComponent("mystore")
	assert.True(t, ok, "component must be committed once init recovers")
	mockStore.AssertNumberOfCalls(t, "Init", 3)
}

// TestProcessorActorStateStoreInitRetryShutdown asserts that shutting the
// processor down while the actor state store init is still retrying stops
// the retry loop promptly and is treated as a clean shutdown, not a fatal
// component init failure.
func TestProcessorActorStateStoreInitRetryShutdown(t *testing.T) {
	proc, reg := newTestProc()

	mockStore := new(daprt.MockStateStore)
	reg.StateStores().RegisterComponent(
		func(logger.Logger) contribstate.Store { return mockStore },
		"mockState",
	)
	firstFailure := make(chan struct{}, 1)
	mockStore.On("Init", mock.Anything, mock.Anything).Run(func(mock.Arguments) {
		select {
		case firstFailure <- struct{}{}:
		default:
		}
	}).Return(errors.New("connection refused"))

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- proc.Process(ctx) }()

	ch := proc.AddPendingComponent(ctx, actorStateStoreComp("mystore"))
	require.NotNil(t, ch)

	select {
	case <-firstFailure:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for the first init failure")
	}
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			require.ErrorIs(t, err, context.Canceled, "an abandoned retry must not surface as a fatal init error")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for the processor to stop")
	}
}

// TestProcessorNonActorStateStoreInitStillFatal guards the pre-existing
// behavior for every other component: a non-ignored init failure is not
// retried and still surfaces as a fatal error out of Process.
func TestProcessorNonActorStateStoreInitStillFatal(t *testing.T) {
	proc, reg := newTestProc()

	mockStore := new(daprt.MockStateStore)
	reg.StateStores().RegisterComponent(
		func(logger.Logger) contribstate.Store { return mockStore },
		"mockState",
	)
	mockStore.On("Init", mock.Anything, mock.Anything).Return(errors.New("connection refused"))

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- proc.Process(ctx) }()

	comp := actorStateStoreComp("plainstore")
	comp.Spec.Metadata = nil

	ch := proc.AddPendingComponent(ctx, comp)
	require.NotNil(t, ch)
	select {
	case err := <-ch:
		require.ErrorContains(t, err, "connection refused")
		mockStore.AssertNumberOfCalls(t, "Init", 1)
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for the init failure")
	}

	cancel()
	select {
	case err := <-errCh:
		require.ErrorContains(t, err, "process component plainstore error")
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for the processor to stop")
	}
}

// TestProcessorActorStateStoreInitRetryStaleCommitRollback: a retriable init
// whose success races a newer spec (phase 1) or a delete (phase 2) must not
// leave the stale component installed: the commit is rolled back and the
// newest event wins, without a fatal init error.
func TestProcessorActorStateStoreInitRetryStaleCommitRollback(t *testing.T) {
	proc, reg := newTestProc()

	mockStore := new(daprt.MockStateStore)
	reg.StateStores().RegisterComponent(
		func(logger.Logger) contribstate.Store { return mockStore },
		"mockState",
	)

	genOf := func(g string) any {
		return mock.MatchedBy(func(md contribstate.Metadata) bool {
			return md.Properties["generation"] == g
		})
	}
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	mockStore.On("Init", genOf("1")).Run(func(mock.Arguments) {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-release
	}).Return(nil)
	mockStore.On("Init", genOf("2")).Return(nil)
	enteredDel := make(chan struct{}, 1)
	releaseDel := make(chan struct{})
	mockStore.On("Init", genOf("d1")).Run(func(mock.Arguments) {
		select {
		case enteredDel <- struct{}{}:
		default:
		}
		<-releaseDel
	}).Return(nil)
	mockStore.On("Close").Maybe().Return(nil)

	withGen := func(name, g string) componentsapi.Component {
		comp := actorStateStoreComp(name)
		comp.Spec.Metadata = append(comp.Spec.Metadata, commonapi.NameValuePair{
			Name: "generation",
			Value: commonapi.DynamicValue{
				JSON: apiextv1.JSON{Raw: []byte(`"` + g + `"`)},
			},
		})
		return comp
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- proc.Process(ctx) }()

	// Phase 1: gen 1 blocks mid-init, gen 2 arrives, then gen 1 succeeds.
	ch1 := proc.AddPendingComponent(ctx, withGen("mystore", "1"))
	require.NotNil(t, ch1)
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for the gen 1 init to start")
	}
	ch2 := proc.AddPendingComponent(ctx, withGen("mystore", "2"))
	require.NotNil(t, ch2)
	close(release)

	select {
	case err := <-ch2:
		require.NoError(t, err, "the newest spec must install")
	case <-time.After(20 * time.Second):
		t.Fatal("timed out waiting for the gen 2 init")
	}
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		comp, ok := proc.compStore.GetComponent("mystore")
		if !assert.True(c, ok) {
			return
		}
		for _, md := range comp.Spec.Metadata {
			if md.Name == "generation" {
				assert.Contains(c, string(md.Value.Raw), "2",
					"the stale gen 1 commit must not survive")
			}
		}
	}, 10*time.Second, 10*time.Millisecond)
	select {
	case err := <-ch1:
		if err != nil {
			require.ErrorContains(t, err, "superseded")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for the gen 1 result")
	}

	// Phase 2: the init blocks, a delete lands, then the init succeeds. The
	// stale commit must be rolled back so the component stays deleted.
	delComp := withGen("delstore", "d1")
	chD := proc.AddPendingComponent(ctx, delComp)
	require.NotNil(t, chD)
	select {
	case <-enteredDel:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for the delstore init to start")
	}
	closeErrCh := make(chan error, 1)
	go func() { closeErrCh <- proc.Close(ctx, delComp) }()
	close(releaseDel)
	select {
	case err := <-closeErrCh:
		require.NoError(t, err)
	case <-time.After(20 * time.Second):
		t.Fatal("timed out waiting for the delete to complete")
	}
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		_, ok := proc.compStore.GetComponent("delstore")
		assert.False(c, ok, "a deleted component must not be re-created by a stale init success")
	}, 10*time.Second, 10*time.Millisecond)

	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			require.ErrorIs(t, err, context.Canceled,
				"a rolled-back stale init must not surface as a fatal error")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for the processor to stop")
	}
}

// TestProcessorActorStateStoreInitRetrySuperseded: a re-create of the same
// name supersedes the old retry loop (phase 1), a delete supersedes the
// successor (phase 2); superseded loops release their caller and are not
// fatal. One shared processor lifecycle: constructing a processor right
// after a component close trips a pre-existing kit loop-segment race under
// -race.
func TestProcessorActorStateStoreInitRetrySuperseded(t *testing.T) {
	proc, reg := newTestProc()

	mockStore := new(daprt.MockStateStore)
	reg.StateStores().RegisterComponent(
		func(logger.Logger) contribstate.Store { return mockStore },
		"mockState",
	)
	initAttempt := make(chan struct{}, 1)
	mockStore.On("Init", mock.Anything, mock.Anything).Run(func(mock.Arguments) {
		select {
		case initAttempt <- struct{}{}:
		default:
		}
	}).Return(errors.New("connection refused"))
	mockStore.On("Close").Maybe().Return(nil)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- proc.Process(ctx) }()

	comp := actorStateStoreComp("mystore")

	// Phase 1: re-create supersedes the old loop.
	oldCh := proc.AddPendingComponent(ctx, comp)
	require.NotNil(t, oldCh)

	select {
	case <-initAttempt:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for the first init failure")
	}

	newCh := proc.AddPendingComponent(ctx, comp)
	require.NotNil(t, newCh)

	select {
	case err := <-oldCh:
		require.Error(t, err)
		require.ErrorContains(t, err, "superseded",
			"the old configuration's retry must stop when a new configuration arrives")
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for the old retry to be superseded")
	}

	// Phase 2: delete supersedes the surviving loop.
	require.NoError(t, proc.Close(ctx, comp))

	select {
	case err := <-newCh:
		require.Error(t, err)
		require.ErrorContains(t, err, "superseded",
			"the pending init must be released as superseded on delete, not left retrying")
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for the deleted component's retry to release its caller")
	}

	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			require.ErrorIs(t, err, context.Canceled,
				"a superseded retry must not surface as a fatal init error")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for the processor to stop")
	}
}
