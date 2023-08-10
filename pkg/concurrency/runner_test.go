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

package concurrency

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_RunnerManager(t *testing.T) {
	t.Parallel()

	t.Run("runner with no tasks should return nil", func(t *testing.T) {
		t.Parallel()

		assert.NoError(t, NewRunnerManager().Run(context.Background()))
	})

	t.Run("runner with a task that completes should return nil", func(t *testing.T) {
		t.Parallel()

		var i int32
		assert.NoError(t, NewRunnerManager(func(ctx context.Context) error {
			atomic.AddInt32(&i, 1)
			return nil
		}).Run(context.Background()))
		assert.Equal(t, int32(1), i)
	})

	t.Run("runner with multiple tasks that complete should return nil", func(t *testing.T) {
		t.Parallel()

		var i int32
		assert.NoError(t, NewRunnerManager(
			func(ctx context.Context) error {
				atomic.AddInt32(&i, 1)
				return nil
			},
			func(ctx context.Context) error {
				atomic.AddInt32(&i, 1)
				return nil
			},
			func(ctx context.Context) error {
				atomic.AddInt32(&i, 1)
				return nil
			},
		).Run(context.Background()))
		assert.Equal(t, int32(3), i)
	})

	t.Run("a runner that errors should error", func(t *testing.T) {
		t.Parallel()

		var i int32
		assert.Error(t, NewRunnerManager(
			func(ctx context.Context) error {
				atomic.AddInt32(&i, 1)
				return errors.New("error")
			},
			func(ctx context.Context) error {
				atomic.AddInt32(&i, 1)
				return nil
			},
			func(ctx context.Context) error {
				atomic.AddInt32(&i, 1)
				return nil
			},
		).Run(context.Background()), errors.New("error"))
		assert.Equal(t, int32(3), i)
	})

	t.Run("a runner with multiple errors should collect all errors (string match)", func(t *testing.T) {
		t.Parallel()

		var i int32
		err := NewRunnerManager(
			func(ctx context.Context) error {
				atomic.AddInt32(&i, 1)
				return errors.New("error")
			},
			func(ctx context.Context) error {
				atomic.AddInt32(&i, 1)
				return errors.New("error")
			},
			func(ctx context.Context) error {
				atomic.AddInt32(&i, 1)
				return errors.New("error")
			},
		).Run(context.Background())
		require.Error(t, err)
		assert.ErrorContains(t, err, "error\nerror\nerror") //nolint:dupword
		assert.Equal(t, int32(3), i)
	})

	t.Run("a runner with multiple errors should collect all errors (unique)", func(t *testing.T) {
		t.Parallel()

		var i int32
		err := NewRunnerManager(
			func(ctx context.Context) error {
				atomic.AddInt32(&i, 1)
				return errors.New("error1")
			},
			func(ctx context.Context) error {
				atomic.AddInt32(&i, 1)
				return errors.New("error2")
			},
			func(ctx context.Context) error {
				atomic.AddInt32(&i, 1)
				return errors.New("error3")
			},
		).Run(context.Background())
		require.Error(t, err)
		assert.ElementsMatch(t, []string{"error1", "error2", "error3"}, strings.Split(err.Error(), "\n"))
		assert.Equal(t, int32(3), i)
	})

	t.Run("should be able to add runner with both New and Add", func(t *testing.T) {
		t.Parallel()

		var i int32
		mngr := NewRunnerManager(
			func(ctx context.Context) error {
				atomic.AddInt32(&i, 1)
				return nil
			},
		)
		assert.NoError(t, mngr.Add(
			func(ctx context.Context) error {
				atomic.AddInt32(&i, 1)
				return nil
			},
		))
		assert.NoError(t, mngr.Add(
			func(ctx context.Context) error {
				atomic.AddInt32(&i, 1)
				return nil
			},
		))
		assert.NoError(t, mngr.Run(context.Background()))
		assert.Equal(t, int32(3), i)
	})

	t.Run("when a runner returns, expect context to be cancelled for other runners", func(t *testing.T) {
		t.Parallel()

		var i int32
		assert.NoError(t, NewRunnerManager(
			func(ctx context.Context) error {
				atomic.AddInt32(&i, 1)
				return nil
			},
			func(ctx context.Context) error {
				atomic.AddInt32(&i, 1)
				select {
				case <-ctx.Done():
				case <-time.After(time.Second):
					t.Error("context should have been cancelled in time")
				}
				return nil
			},
			func(ctx context.Context) error {
				atomic.AddInt32(&i, 1)
				select {
				case <-ctx.Done():
				case <-time.After(time.Second):
					t.Error("context should have been cancelled in time")
				}
				return nil
			},
		).Run(context.Background()))
		assert.Equal(t, int32(3), i)
	})

	t.Run("when a runner errors, expect context to be cancelled for other runners", func(t *testing.T) {
		t.Parallel()

		var i int32
		err := NewRunnerManager(
			func(ctx context.Context) error {
				atomic.AddInt32(&i, 1)
				select {
				case <-ctx.Done():
				case <-time.After(time.Second):
					t.Error("context should have been cancelled in time")
				}
				return errors.New("error1")
			},
			func(ctx context.Context) error {
				atomic.AddInt32(&i, 1)
				select {
				case <-ctx.Done():
				case <-time.After(time.Second):
					t.Error("context should have been cancelled in time")
				}
				return errors.New("error2")
			},
			func(ctx context.Context) error {
				atomic.AddInt32(&i, 1)
				return errors.New("error3")
			},
		).Run(context.Background())
		require.Error(t, err)
		assert.ElementsMatch(t, []string{"error1", "error2", "error3"}, strings.Split(err.Error(), "\n"))
		assert.Equal(t, int32(3), i)
	})

	t.Run("a manger started twice should error", func(t *testing.T) {
		t.Parallel()

		var i int32
		m := NewRunnerManager(func(ctx context.Context) error {
			atomic.AddInt32(&i, 1)
			return nil
		})
		assert.NoError(t, m.Run(context.Background()))
		assert.Equal(t, int32(1), i)
		assert.Error(t, m.Run(context.Background()), errors.New("manager already started"))
		assert.Equal(t, int32(1), i)
	})

	t.Run("adding a task to a started manager should error", func(t *testing.T) {
		t.Parallel()

		var i int32
		m := NewRunnerManager(func(ctx context.Context) error {
			atomic.AddInt32(&i, 1)
			return nil
		})
		assert.NoError(t, m.Run(context.Background()))
		assert.Equal(t, int32(1), i)
		err := m.Add(func(ctx context.Context) error {
			atomic.AddInt32(&i, 1)
			return nil
		})
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrManagerAlreadyStarted)
		assert.Equal(t, int32(1), i)
	})
}
