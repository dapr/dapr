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

package input

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/components-contrib/bindings"
	"github.com/dapr/components-contrib/metadata"
)

type streamingBinding struct {
	bindings.InputBinding
	wg sync.WaitGroup
}

func (s *streamingBinding) Init(context.Context, bindings.Metadata) error { return nil }

func (s *streamingBinding) Read(ctx context.Context, handler bindings.Handler) error {
	for range 4 {
		s.wg.Go(func() {
			for ctx.Err() == nil {
				_, _ = handler(ctx, &bindings.ReadResponse{Data: []byte("x")})
			}
		})
	}
	return nil
}

func (s *streamingBinding) GetComponentMetadata() metadata.MetadataMap { return nil }

func TestStop_ConcurrentWithEvents(t *testing.T) {
	t.Parallel()

	for i := range 20 {
		b := new(streamingBinding)

		var afterStop atomic.Bool
		var calls, lateCalls atomic.Int64
		in, err := Run(Options{
			Name:    "test",
			Binding: b,
			Handler: func(context.Context, string, []byte, map[string]string) ([]byte, error) {
				calls.Add(1)
				if afterStop.Load() {
					lateCalls.Add(1)
				}
				return nil, nil
			},
		})
		require.NoError(t, err)

		require.Eventually(t, func() bool { return calls.Load() > 0 }, 5*time.Second, time.Microsecond)
		in.Stop()
		afterStop.Store(true)

		done := make(chan struct{})
		go func() {
			b.wg.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			require.FailNow(t, "binding read loops did not stop", "iteration %d", i)
		}

		assert.Equal(t, int64(0), lateCalls.Load(),
			"iteration %d: no event may reach the app after Stop returns", i)
	}
}

func TestStop_WaitsForInflightEvent(t *testing.T) {
	t.Parallel()

	b := new(streamingBinding)
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	var finished atomic.Bool

	in, err := Run(Options{
		Name:    "test",
		Binding: b,
		Handler: func(context.Context, string, []byte, map[string]string) ([]byte, error) {
			once.Do(func() {
				close(started)
				<-release
				finished.Store(true)
			})
			return nil, nil
		},
	})
	require.NoError(t, err)

	<-started
	stopped := make(chan struct{})
	go func() {
		in.Stop()
		close(stopped)
	}()

	select {
	case <-stopped:
		require.FailNow(t, "Stop returned while an event was still being handled")
	case <-time.After(100 * time.Millisecond):
	}

	close(release)
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		require.FailNow(t, "Stop did not return")
	}
	assert.True(t, finished.Load())
}

func TestStop_Idempotent(t *testing.T) {
	t.Parallel()

	in, err := Run(Options{
		Name:    "test",
		Binding: new(streamingBinding),
		Handler: func(context.Context, string, []byte, map[string]string) ([]byte, error) {
			return nil, nil
		},
	})
	require.NoError(t, err)

	in.Stop()
	in.Stop()
}

type manualBinding struct {
	bindings.InputBinding
	handler bindings.Handler
}

func (m *manualBinding) Read(_ context.Context, handler bindings.Handler) error {
	m.handler = handler
	return nil
}

func TestStop_GracePeriodForInflightEvent(t *testing.T) {
	t.Parallel()

	b := new(manualBinding)
	started := make(chan struct{})
	release := make(chan struct{})
	in, err := Run(Options{
		Name:    "test",
		Binding: b,
		Handler: func(context.Context, string, []byte, map[string]string) ([]byte, error) {
			close(started)
			<-release
			return nil, nil
		},
	})
	require.NoError(t, err)

	go b.handler(t.Context(), &bindings.ReadResponse{})
	<-started

	stopped := make(chan time.Time)
	go func() {
		in.Stop()
		stopped <- time.Now()
	}()

	time.Sleep(50 * time.Millisecond)
	released := time.Now()
	close(release)

	select {
	case at := <-stopped:
		assert.GreaterOrEqual(t, at.Sub(released), 400*time.Millisecond)
	case <-time.After(5 * time.Second):
		require.FailNow(t, "Stop did not return")
	}
}

func TestStop_NewEventsFailFastWhileStopping(t *testing.T) {
	t.Parallel()

	b := new(manualBinding)
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	var appCalls atomic.Int64
	in, err := Run(Options{
		Name:    "test",
		Binding: b,
		Handler: func(context.Context, string, []byte, map[string]string) ([]byte, error) {
			appCalls.Add(1)
			once.Do(func() {
				close(started)
				<-release
			})
			return nil, nil
		},
	})
	require.NoError(t, err)

	go b.handler(t.Context(), &bindings.ReadResponse{})
	<-started

	stopped := make(chan struct{})
	go func() {
		in.Stop()
		close(stopped)
	}()

	require.Eventually(t, in.closed.Load, 5*time.Second, time.Millisecond)

	errCh := make(chan error, 1)
	go func() {
		_, err := b.handler(t.Context(), &bindings.ReadResponse{})
		errCh <- err
	}()
	select {
	case err := <-errCh:
		require.EqualError(t, err, "input binding is closed")
	case <-time.After(time.Second):
		require.FailNow(t, "new event blocked while Stop was waiting")
	}

	close(release)
	<-stopped
	assert.Equal(t, int64(1), appCalls.Load())
}
