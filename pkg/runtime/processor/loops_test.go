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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/dapr/components-contrib/pubsub"
	"github.com/dapr/components-contrib/secretstores"
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

func startProcExpectingErr(t *testing.T, proc *Processor) func() error {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	errCh := make(chan error, 1)
	go func() {
		errCh <- proc.Process(ctx)
	}()
	return func() error {
		cancel()
		select {
		case err := <-errCh:
			return err
		case <-time.After(5 * time.Second):
			t.Fatal("processor.Process did not return in time")
			return nil
		}
	}
}

// TestProcessorSecretStoreDependentNeverLoadedFailsFlush asserts that if a component
// references a secret store that is never loaded, Flush fails and returns an error
// stating that the component references an unready secret store.
func TestProcessorSecretStoreDependentNeverLoadedFailsFlush(t *testing.T) {
	proc, reg := newTestProc()
	stop := startProcExpectingErr(t, proc)
	defer func() {
		_ = stop()
	}()

	mockPubSub := new(daprt.MockPubSub)
	reg.PubSubs().RegisterComponent(
		func(logger.Logger) pubsub.PubSub { return mockPubSub },
		"mockPubSub",
	)
	mockPubSub.On("Init", mock.Anything).Return(nil)

	dependent := inlinePubsubComp("needs-missing-secret")
	dependent.SecretStore = "never-loaded-store"
	dependent.Spec.Metadata = append(dependent.Spec.Metadata, commonapi.NameValuePair{
		Name:         "password",
		SecretKeyRef: commonapi.SecretKeyRef{Name: "mysecret", Key: "key1"},
	})

	ch := proc.AddPendingComponent(t.Context(), dependent)
	require.NotNil(t, ch)
	require.NoError(t, <-ch)

	err := proc.Flush(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "references unready secret store never-loaded-store")
	assert.Error(t, proc.FatalError())
}

// TestProcessorSecretStoreDependentIgnoreErrorsPassesFlush asserts that if a component
// references an unready secret store that is never loaded, but specifies IgnoreErrors=true,
// Flush ignores the error and returns nil.
func TestProcessorSecretStoreDependentIgnoreErrorsPassesFlush(t *testing.T) {
	proc, reg := newTestProc()
	startProc(t, proc)

	mockPubSub := new(daprt.MockPubSub)
	reg.PubSubs().RegisterComponent(
		func(logger.Logger) pubsub.PubSub { return mockPubSub },
		"mockPubSub",
	)
	mockPubSub.On("Init", mock.Anything).Return(nil)

	dependent := inlinePubsubComp("ignored-needs-missing-secret")
	dependent.SecretStore = "never-loaded-store"
	dependent.Spec.IgnoreErrors = true
	dependent.Spec.Metadata = append(dependent.Spec.Metadata, commonapi.NameValuePair{
		Name:         "password",
		SecretKeyRef: commonapi.SecretKeyRef{Name: "mysecret", Key: "key1"},
	})

	ch := proc.AddPendingComponent(t.Context(), dependent)
	require.NotNil(t, ch)
	require.NoError(t, <-ch)

	require.NoError(t, proc.Flush(t.Context()))
	assert.NoError(t, proc.FatalError())
}

// TestProcessorSecretStoreDependentInitFailureFailsFlush asserts that if a component
// is deferred behind a secret store, and that secret store is loaded, but the dependent
// component's init fails, Flush surfaces the fatal init error.
func TestProcessorSecretStoreDependentInitFailureFailsFlush(t *testing.T) {
	proc, reg := newTestProc()
	stop := startProcExpectingErr(t, proc)
	defer func() {
		_ = stop()
	}()

	mockPubSub := new(daprt.MockPubSub)
	reg.PubSubs().RegisterComponent(
		func(logger.Logger) pubsub.PubSub { return mockPubSub },
		"mockPubSub",
	)
	mockPubSub.On("Init", mock.Anything).Return(assert.AnError)
	reg.SecretStores().RegisterComponent(
		func(logger.Logger) secretstores.SecretStore { return rtmock.NewMockKubernetesStore() },
		"mockSecret",
	)

	dependent := inlinePubsubComp("failing-dependent")
	dependent.SecretStore = "mystore"
	dependent.Spec.Metadata = append(dependent.Spec.Metadata, commonapi.NameValuePair{
		Name:         "password",
		SecretKeyRef: commonapi.SecretKeyRef{Name: "mysecret", Key: "key1"},
	})

	ch := proc.AddPendingComponent(t.Context(), dependent)
	require.NotNil(t, ch)
	require.NoError(t, <-ch)

	require.NoError(t, proc.Init(t.Context(), componentsapi.Component{
		ObjectMeta: metav1.ObjectMeta{Name: "mystore"},
		Spec:       componentsapi.ComponentSpec{Type: "secretstores.mockSecret", Version: "v1"},
	}))

	err := proc.Flush(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), assert.AnError.Error())
	assert.Error(t, proc.FatalError())
}
