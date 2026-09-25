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

package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeServer struct {
	run func(ctx context.Context) error
}

func (f *fakeServer) Run(ctx context.Context) error {
	return f.run(ctx)
}

func Test_runServerLoop(t *testing.T) {
	t.Parallel()

	t.Run("getServer error is fatal", func(t *testing.T) {
		t.Parallel()

		fatal := errors.New("bad config")
		err := runServerLoop(t.Context(), func() (serverRunner, error) {
			return nil, fatal
		})
		require.ErrorIs(t, err, fatal)
	})

	t.Run("runtime failures are retried until the server recovers", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		var runs int
		recovered := make(chan struct{})
		done := make(chan error, 1)
		go func() {
			done <- runServerLoop(ctx, func() (serverRunner, error) {
				return &fakeServer{run: func(ctx context.Context) error {
					runs++
					if runs < 3 {
						return errors.New("connecting to embedded etcd: no route to host")
					}
					close(recovered)
					<-ctx.Done()
					return ctx.Err()
				}}, nil
			})
		}()

		select {
		case <-recovered:
		case err := <-done:
			t.Fatalf("loop exited instead of retrying: %v", err)
		case <-time.After(30 * time.Second):
			t.Fatal("timed out waiting for the server to recover")
		}
		assert.Equal(t, 3, runs)

		cancel()
		select {
		case err := <-done:
			require.ErrorIs(t, err, context.Canceled)
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for the loop to stop")
		}
	})

	t.Run("context cancellation stops the retry backoff wait", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		failing := make(chan struct{}, 1)
		done := make(chan error, 1)
		go func() {
			done <- runServerLoop(ctx, func() (serverRunner, error) {
				return &fakeServer{run: func(context.Context) error {
					select {
					case failing <- struct{}{}:
					default:
					}
					return errors.New("still down")
				}}, nil
			})
		}()

		select {
		case <-failing:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for the first failure")
		}
		cancel()

		select {
		case err := <-done:
			require.ErrorIs(t, err, context.Canceled)
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for the loop to stop after cancellation")
		}
	})

	t.Run("nil run result recreates the server without backoff", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		var runs int
		done := make(chan error, 1)
		go func() {
			done <- runServerLoop(ctx, func() (serverRunner, error) {
				return &fakeServer{run: func(ctx context.Context) error {
					runs++
					if runs < 3 {
						return nil
					}
					cancel()
					return ctx.Err()
				}}, nil
			})
		}()

		select {
		case err := <-done:
			require.ErrorIs(t, err, context.Canceled)
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for the loop to stop")
		}
		assert.Equal(t, 3, runs)
	})
}
