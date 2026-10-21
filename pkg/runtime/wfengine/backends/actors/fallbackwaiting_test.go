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

package actors

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	clocktesting "k8s.io/utils/clock/testing"

	diag "github.com/dapr/dapr/pkg/diagnostics"
)

func TestSetFallbackHost(t *testing.T) {
	abe, _ := newFallbackTestBackend(t, true, false)

	// Not a fallback host: nothing is counted.
	abe.fallbackServed(diag.CreateWorkflow)
	assert.Nil(t, abe.fallbackWaiting.Load())

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	abe.SetFallbackHost(ctx, true)
	w := abe.fallbackWaiting.Load()
	require.NotNil(t, w)
	abe.fallbackServed(diag.CreateWorkflow)
	assert.EqualValues(t, 1, w.waiting.Load())

	// Idempotent: the count carries on.
	abe.SetFallbackHost(ctx, true)
	assert.Same(t, w, abe.fallbackWaiting.Load())

	// Leaving the fallback state stops the warnings and the count; becoming
	// a fallback host again starts afresh.
	abe.SetFallbackHost(ctx, false)
	assert.Nil(t, abe.fallbackWaiting.Load())
	abe.SetFallbackHost(ctx, true)
	require.NotNil(t, abe.fallbackWaiting.Load())
	assert.NotSame(t, w, abe.fallbackWaiting.Load())
	assert.Zero(t, abe.fallbackWaiting.Load().waiting.Load())

	// Never once ctx is done.
	abe.SetFallbackHost(ctx, false)
	cancel()
	abe.SetFallbackHost(ctx, true)
	assert.Nil(t, abe.fallbackWaiting.Load())

	off, _ := newFallbackTestBackend(t, false, false)
	assert.Nil(t, off.fallbackFunc())
}

func TestFallbackWaiting(t *testing.T) {
	type warning struct {
		appID   string
		waiting int64
		since   time.Duration
	}

	clock := clocktesting.NewFakeClock(time.Now())
	warnings := make(chan warning, 10)
	w := newFallbackWaiting("myapp")
	w.clock = clock
	w.warn = func(appID string, waiting int64, since time.Duration) {
		warnings <- warning{appID, waiting, since}
	}

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		w.run(ctx)
		close(done)
	}()

	// step advances the clock to elapsed since the start, once run waits on
	// its next warning.
	var elapsed time.Duration
	step := func(to time.Duration) {
		t.Helper()
		require.Eventually(t, clock.HasWaiters, 10*time.Second, time.Millisecond)
		clock.Step(to - elapsed)
		elapsed = to
	}
	noWarning := func() {
		t.Helper()
		require.Eventually(t, clock.HasWaiters, 10*time.Second, time.Millisecond)
		select {
		case got := <-warnings:
			require.Failf(t, "unexpected warning", "%+v", got)
		default:
		}
	}

	// Nothing served: no warning at the threshold, nor at the next one.
	step(fallbackWaitingWarnAfter)
	noWarning()
	step(2 * fallbackWaitingWarnAfter)
	noWarning()

	w.served()
	w.served()

	// Warnings come at doubling intervals since the start, capped at
	// fallbackWaitingWarnMaxInterval: 5m and 10m (passed above), 20m, 40m,
	// 80m, 140m.
	for _, at := range []time.Duration{20, 40, 80, 140} {
		at *= time.Minute
		step(at - time.Second)
		noWarning()
		step(at)
		select {
		case got := <-warnings:
			assert.Equal(t, warning{"myapp", 2, at}, got)
		case <-time.After(10 * time.Second):
			require.Failf(t, "no warning", "at %s", at)
		}
	}

	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		require.Fail(t, "run did not stop")
	}
}
