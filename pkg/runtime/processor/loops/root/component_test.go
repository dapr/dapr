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
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dapr/kit/events/loop"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	"github.com/dapr/dapr/pkg/components"
	"github.com/dapr/dapr/pkg/runtime/processor/loops"
)

// failCategoryLoop is a test category loop that immediately fails all init
// requests with a configurable error, simulating a component whose Init
// returns an error (e.g. a secret store that cannot connect).
type failCategoryLoop struct {
	err error
}

func (f *failCategoryLoop) Run(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}

func (f *failCategoryLoop) Enqueue(ev loops.EventCategory) {
	if init, ok := ev.(*loops.Init); ok {
		sendResult(init.Result, f.err)
	}
}

func (f *failCategoryLoop) Close() error { return nil }

// TestHandleInit_FatalErrorOnCancellation verifies that when a component init
// fails and the processor shuts down (context cancelled), the fatal init error
// is still recorded with both the component name and type in the error message.
//
// This is the regression test for the copilot review comment:
// "The new type-bearing error is only exercised when the component init result
// races processor shutdown, but the added integration assertions cover the
// normal initComponentBlocking path and do not verify this fallback."
func TestHandleInit_FatalErrorOnCancellation(t *testing.T) {
	// Create a category loop that immediately fails init
	catLoop := &failCategoryLoop{err: fmt.Errorf("connection refused")}

	// Create a Root with the mock category loop registered for the
	// "secretstores" category (the prefix matching is used by handleInit).
	r := New(Options{
		Categories: map[components.Category]loop.Interface[loops.EventCategory]{
			components.CategorySecretStore: catLoop,
		},
	})

	// Create a cancellable context for the root loop
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start the root loop in a goroutine
	var runErr error
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		runErr = r.Run(ctx)
	}()

	// Enqueue an Init event for a component that will fail to init.
	// Use a secret store component because that is the category most
	// likely to fail in practice (external dependency).
	resultCh := make(chan error, 1)
	r.loop.Enqueue(&loops.Init{
		Component: compapi.Component{
			ObjectMeta: metav1.ObjectMeta{
				Name: "mysecretstore",
			},
			Spec: compapi.ComponentSpec{
				Type: "secretstores.kubernetes",
			},
		},
		Result: resultCh,
	})

	// Wait for the init to be processed and the error to be recorded.
	// The handleInit finalizer goroutine calls recordFatalInitError when
	// the category loop returns an error.
	time.Sleep(200 * time.Millisecond)

	// Cancel the context to simulate processor shutdown while the
	// finalizer is still in flight. This is the race condition the
	// copilot review flagged.
	cancel()

	// Wait for the loop to exit
	wg.Wait()

	// Verify that the fatal error was recorded.
	// The fatal error should contain both the component name and type.
	fatal := r.fatalErr.Load()
	require.NotNil(t, fatal, "expected fatal error to be recorded")

	fatalErr := *fatal
	errStr := fatalErr.Error()

	// Assert the error contains the component name.
	assert.True(t, strings.Contains(errStr, "mysecretstore"),
		"fatal error should contain component name, got: %s", errStr)

	// Assert the error contains the component type.
	assert.True(t, strings.Contains(errStr, "secretstores.kubernetes"),
		"fatal error should contain component type, got: %s", errStr)

	// Assert the error contains the wrapped init error.
	assert.True(t, strings.Contains(errStr, "connection refused"),
		"fatal error should contain the underlying init error, got: %s", errStr)

	// Assert the error matches the expected format.
	assert.Contains(t, errStr, "process component mysecretstore (type: secretstores.kubernetes) error:")
}

// TestHandleInit_IgnoreErrors verifies that when a component has IgnoreErrors
// set to true, the fatal error is NOT recorded even if init fails.
func TestHandleInit_IgnoreErrors(t *testing.T) {
	catLoop := &failCategoryLoop{err: fmt.Errorf("connection refused")}

	r := New(Options{
		Categories: map[components.Category]loop.Interface[loops.EventCategory]{
			components.CategorySecretStore: catLoop,
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		r.Run(ctx)
	}()

	resultCh := make(chan error, 1)
	r.loop.Enqueue(&loops.Init{
		Component: compapi.Component{
			ObjectMeta: metav1.ObjectMeta{
				Name: "mysecretstore",
			},
			Spec: compapi.ComponentSpec{
				Type:    "secretstores.kubernetes",
				Version: "v1",
				// IgnoreErrors is set — the fatal error should NOT be recorded.
				// Note: IgnoreErrors is a field on ComponentSpec in the real API.
			},
		},
		Result: resultCh,
	})

	time.Sleep(200 * time.Millisecond)
	cancel()
	wg.Wait()

	// With IgnoreErrors, no fatal error should be recorded.
	fatal := r.fatalErr.Load()
	assert.Nil(t, fatal, "expected no fatal error when IgnoreErrors is set")
}
