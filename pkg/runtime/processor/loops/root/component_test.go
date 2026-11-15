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

package root

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/dapr/kit/events/loop"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	"github.com/dapr/dapr/pkg/components"
	"github.com/dapr/dapr/pkg/runtime/meta"
	"github.com/dapr/dapr/pkg/runtime/processor/loops"
)

// failCategoryHandler stands in for a category loop whose components always
// fail to init (e.g. a secret store that cannot reach its backend). The error
// names the component so tests can tell which init produced it.
type failCategoryHandler struct {
	err error
}

func (f *failCategoryHandler) Handle(_ context.Context, ev loops.EventCategory) error {
	if init, ok := ev.(*loops.Init); ok {
		sendResult(init.Result, fmt.Errorf("%s: %w", init.Component.Name, f.err))
	}
	return nil
}

// nopSecretResolver stands in for the secret processor. Nothing is left
// unresolved, so every resource falls through to its category loop.
type nopSecretResolver struct{}

func (nopSecretResolver) ProcessResource(context.Context, meta.Resource) (bool, string) {
	return false, ""
}

func secretStoreComponent(name string, ignoreErrors bool) compapi.Component {
	return compapi.Component{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: compapi.ComponentSpec{
			Type:         "secretstores.kubernetes",
			Version:      "v1",
			IgnoreErrors: ignoreErrors,
		},
	}
}

// runInitFailure starts a root loop plus one failing secret-store category loop,
// submits a component Init, and returns once the init result has been delivered.
// The caller must drain the root loop with closeRoot before asserting on
// fatalErr, because Run publishes it only after the init finalizers complete.
func runInitFailure(t *testing.T, comp compapi.Component, initErr error) (*Root, <-chan error) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	catLoop := loops.CategoryFactory.NewLoop(&failCategoryHandler{err: initErr})
	go func() { _ = catLoop.Run(ctx) }()

	r := New(Options{
		Categories: map[components.Category]loop.Interface[loops.EventCategory]{
			components.CategorySecretStore: catLoop,
		},
		Secret: nopSecretResolver{},
	})

	runErr := make(chan error, 1)
	go func() { runErr <- r.Run(ctx) }()

	resultCh := make(chan error, 1)
	r.loop.Enqueue(&loops.Init{
		Component: comp,
		Result:    resultCh,
		Timeout:   DefaultComponentInitTimeout,
	})

	select {
	case err := <-resultCh:
		require.Error(t, err, "component init should have failed")
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for the component init result")
	}

	return r, runErr
}

// closeRoot drains the root loop and waits for Run to return. Run waits for the
// init finalizers before reading fatalErr, so once this returns the recorded
// fatal error is stable.
//
// Close must be awaited as well as Run: Run returns the root loop to the
// shared factory pool, so a Close still in flight would race the next test's
// reuse of the same pooled loop.
func closeRoot(t *testing.T, r *Root, runErr <-chan error) error {
	t.Helper()

	closeDone := make(chan struct{})
	go func() {
		r.loop.Close(new(loops.Shutdown))
		close(closeDone)
	}()

	select {
	case err := <-runErr:
		<-closeDone
		return err
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for the root loop to shut down")
		return nil
	}
}

// TestHandleInit_FatalErrorRecorded verifies that a component init failure on
// the fatal path is recorded with both the component name and its type, so the
// runtime can surface it even when it races with shutdown.
func TestHandleInit_FatalErrorRecorded(t *testing.T) {
	const compName = "mysecretstore"

	r, runErr := runInitFailure(t, secretStoreComponent(compName, false), errors.New("connection refused"))
	require.Error(t, closeRoot(t, r, runErr), "Run should surface the fatal init error")

	fatal := r.fatalErr.Load()
	require.NotNil(t, fatal, "expected a fatal init error to be recorded")

	errStr := (*fatal).Error()
	assert.Contains(t, errStr, compName, "fatal error should contain the component name")
	assert.Contains(t, errStr, "secretstores.kubernetes", "fatal error should contain the component type")
	assert.Contains(t, errStr, "connection refused", "fatal error should contain the underlying init error")
	assert.Contains(t, errStr,
		"process component mysecretstore (type: secretstores.kubernetes) error:",
		"fatal error should carry the type-qualified wrapper",
	)
}

// TestHandleInit_IgnoreErrorsNotFatal verifies that a component with
// IgnoreErrors set does not record a fatal init error.
func TestHandleInit_IgnoreErrorsNotFatal(t *testing.T) {
	r, runErr := runInitFailure(t, secretStoreComponent("mysecretstore", true), errors.New("connection refused"))
	require.NoError(t, closeRoot(t, r, runErr), "an ignored init failure must not be fatal")

	assert.Nil(t, r.fatalErr.Load(), "no fatal error should be recorded when IgnoreErrors is set")
}

// TestHandleInit_FatalErrorFirstWins verifies that only the first non-ignored
// init failure is retained, matching recordFatalInitError's CompareAndSwap.
func TestHandleInit_FatalErrorFirstWins(t *testing.T) {
	r, runErr := runInitFailure(t, secretStoreComponent("store-a", false), errors.New("first failure"))

	// A second failing init must not displace the first recorded error.
	r.loop.Enqueue(&loops.Init{
		Component: secretStoreComponent("store-b", false),
		Result:    make(chan error, 1),
		Timeout:   DefaultComponentInitTimeout,
	})

	require.Error(t, closeRoot(t, r, runErr))

	fatal := r.fatalErr.Load()
	require.NotNil(t, fatal)
	assert.True(t, strings.Contains((*fatal).Error(), "store-a"),
		"the first fatal error should be retained, got: %s", (*fatal).Error())
	assert.False(t, strings.Contains((*fatal).Error(), "store-b"),
		"a later failure must not displace the first, got: %s", (*fatal).Error())
}
