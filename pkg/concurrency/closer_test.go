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
	clocktesting "k8s.io/utils/clock/testing"
)

type mockCloser func() error

func (m mockCloser) Close() error {
	return m()
}

func Test_RunnerClosterManager(t *testing.T) {
	t.Parallel()

	t.Run("runner with no tasks or closers should return nil", func(t *testing.T) {
		t.Parallel()

		assert.NoError(t, NewRunnerCloserManager(nil).Run(context.Background()))
	})

	t.Run("runner with a task that completes should return nil", func(t *testing.T) {
		t.Parallel()

		var i atomic.Int32
		assert.NoError(t, NewRunnerCloserManager(nil, func(context.Context) error {
			i.Add(1)
			return nil
		}).Run(context.Background()))
		assert.Equal(t, int32(1), i.Load())
	})

	t.Run("runner with a task and closer that completes should return nil", func(t *testing.T) {
		t.Parallel()

		var i atomic.Int32
		mngr := NewRunnerCloserManager(nil, func(context.Context) error {
			i.Add(1)
			return nil
		})
		assert.NoError(t, mngr.AddCloser(func(context.Context) error {
			i.Add(1)
			return nil
		}))
		assert.NoError(t, mngr.Run(context.Background()))
		assert.Equal(t, int32(2), i.Load())
	})

	t.Run("runner with multiple tasks and closers that complete should return nil", func(t *testing.T) {
		t.Parallel()

		var i atomic.Int32
		mngr := NewRunnerCloserManager(nil,
			func(context.Context) error {
				i.Add(1)
				return nil
			},
			func(context.Context) error {
				i.Add(1)
				return nil
			},
			func(context.Context) error {
				i.Add(1)
				return nil
			},
		)
		assert.NoError(t, mngr.AddCloser(
			func(context.Context) error {
				i.Add(1)
				return nil
			},
			func() error {
				i.Add(1)
				return nil
			},
			func() {
				i.Add(1)
			},
			mockCloser(func() error {
				i.Add(1)
				return nil
			}),
		))

		assert.NoError(t, mngr.Run(context.Background()))
		assert.Equal(t, int32(7), i.Load())
	})

	t.Run("a runner that errors should error but still call the closers", func(t *testing.T) {
		t.Parallel()

		var i atomic.Int32
		mngr := NewRunnerCloserManager(nil,
			func(ctx context.Context) error {
				i.Add(1)
				return errors.New("error")
			},
			func(ctx context.Context) error {
				i.Add(1)
				return nil
			},
			func(ctx context.Context) error {
				i.Add(1)
				return nil
			},
		)
		assert.NoError(t, mngr.AddCloser(
			func(ctx context.Context) error {
				i.Add(1)
				return nil
			},
		))

		assert.EqualError(t, mngr.Run(context.Background()), "error")
		assert.Equal(t, int32(4), i.Load())
	})

	t.Run("a runner that has closter errors should error", func(t *testing.T) {
		t.Parallel()

		var i atomic.Int32
		mngr := NewRunnerCloserManager(nil,
			func(ctx context.Context) error {
				i.Add(1)
				return nil
			},
			func(ctx context.Context) error {
				i.Add(1)
				return nil
			},
			func(ctx context.Context) error {
				i.Add(1)
				return nil
			},
		)
		assert.NoError(t, mngr.AddCloser(
			func(ctx context.Context) error {
				i.Add(1)
				return errors.New("error")
			},
		))

		assert.EqualError(t, mngr.Run(context.Background()), "error")
		assert.Equal(t, int32(4), i.Load())
	})

	t.Run("a runner with multiple errors should collect all errors (string match)", func(t *testing.T) {
		t.Parallel()

		var i atomic.Int32
		mngr := NewRunnerCloserManager(nil,
			func(ctx context.Context) error {
				i.Add(1)
				return errors.New("error")
			},
			func(ctx context.Context) error {
				i.Add(1)
				return errors.New("error")
			},
			func(ctx context.Context) error {
				i.Add(1)
				return errors.New("error")
			},
		)
		assert.NoError(t, mngr.AddCloser(
			func(ctx context.Context) error {
				i.Add(1)
				return errors.New("closererror")
			},
			func() error {
				i.Add(1)
				return errors.New("closererror")
			},
			mockCloser(func() error {
				i.Add(1)
				return errors.New("closererror")
			}),
		))

		err := mngr.Run(context.Background())
		require.Error(t, err)
		assert.ErrorContains(t, err, "error\nerror\nerror\nclosererror\nclosererror\nclosererror") //nolint:dupword
		assert.Equal(t, int32(6), i.Load())
	})

	t.Run("a runner with multiple errors should collect all errors (unique)", func(t *testing.T) {
		t.Parallel()

		var i atomic.Int32
		mngr := NewRunnerCloserManager(nil,
			func(ctx context.Context) error {
				i.Add(1)
				return errors.New("error1")
			},
			func(ctx context.Context) error {
				i.Add(1)
				return errors.New("error2")
			},
			func(ctx context.Context) error {
				i.Add(1)
				return errors.New("error3")
			},
		)
		assert.NoError(t, mngr.AddCloser(
			func(ctx context.Context) error {
				i.Add(1)
				return errors.New("closererror1")
			},
			func() error {
				i.Add(1)
				return errors.New("closererror2")
			},
			mockCloser(func() error {
				i.Add(1)
				return errors.New("closererror3")
			}),
		))

		err := mngr.Run(context.Background())
		require.Error(t, err)
		assert.ElementsMatch(t,
			[]string{"error1", "error2", "error3", "closererror1", "closererror2", "closererror3"},
			strings.Split(err.Error(), "\n"),
		)
		assert.Equal(t, int32(6), i.Load())
	})

	t.Run("should be able to add runner with New, Add and AddCloser", func(t *testing.T) {
		t.Parallel()

		var i atomic.Int32
		mngr := NewRunnerCloserManager(nil,
			func(ctx context.Context) error {
				i.Add(1)
				return nil
			},
		)
		assert.NoError(t, mngr.Add(
			func(ctx context.Context) error {
				i.Add(1)
				return nil
			},
		))
		assert.NoError(t, mngr.Add(
			func(ctx context.Context) error {
				i.Add(1)
				return nil
			},
		))
		assert.NoError(t, mngr.AddCloser(
			func(ctx context.Context) error {
				i.Add(1)
				return nil
			},
		))
		assert.NoError(t, mngr.AddCloser(
			func() {
				i.Add(1)
			},
		))

		assert.NoError(t, mngr.Run(context.Background()))
		assert.Equal(t, int32(5), i.Load())
	})

	t.Run("when a runner returns, expect context to be cancelled for other runners, but not for closers returning", func(t *testing.T) {
		t.Parallel()

		var i atomic.Int32
		mngr := NewRunnerCloserManager(nil,
			func(ctx context.Context) error {
				i.Add(1)
				return nil
			},
			func(ctx context.Context) error {
				i.Add(1)
				select {
				case <-ctx.Done():
				case <-time.After(time.Second):
					t.Error("context should have been cancelled in time")
				}
				return nil
			},
			func(ctx context.Context) error {
				i.Add(1)
				select {
				case <-ctx.Done():
				case <-time.After(time.Second):
					t.Error("context should have been cancelled in time")
				}
				return nil
			},
		)

		closer1Ch := make(chan struct{})
		closer2Ch := make(chan struct{})
		assert.NoError(t, mngr.AddCloser(
			func(ctx context.Context) error {
				i.Add(1)
				close(closer1Ch)
				return nil
			},
			func(ctx context.Context) error {
				i.Add(1)
				select {
				case <-ctx.Done():
					t.Error("context should not have been cancelled")
				case <-closer1Ch:
				}
				close(closer2Ch)
				return nil
			},
			func(ctx context.Context) error {
				i.Add(1)
				select {
				case <-ctx.Done():
					t.Error("context should not have been cancelled")
				case <-closer2Ch:
				}
				return nil
			},
		))

		assert.NoError(t, mngr.Run(context.Background()))
		assert.Equal(t, int32(6), i.Load())
	})

	t.Run("when a runner errors, expect context to be cancelled for other runners, but closers should still run", func(t *testing.T) {
		t.Parallel()

		var i atomic.Int32
		mngr := NewRunnerCloserManager(nil,
			func(ctx context.Context) error {
				i.Add(1)
				select {
				case <-ctx.Done():
				case <-time.After(time.Second):
					t.Error("context should have been cancelled in time")
				}
				return errors.New("error1")
			},
			func(ctx context.Context) error {
				i.Add(1)
				select {
				case <-ctx.Done():
				case <-time.After(time.Second):
					t.Error("context should have been cancelled in time")
				}
				return errors.New("error2")
			},
			func(ctx context.Context) error {
				i.Add(1)
				return errors.New("error3")
			},
		)

		assert.NoError(t, mngr.AddCloser(
			func(ctx context.Context) error {
				i.Add(1)
				select {
				case <-ctx.Done():
					t.Error("context should not have been cancelled")
				default:
				}
				return errors.New("closererror1")
			},
			func(ctx context.Context) error {
				i.Add(1)
				select {
				case <-ctx.Done():
					t.Error("context should not have been cancelled")
				default:
				}
				return errors.New("closererror2")
			},
		))

		err := mngr.Run(context.Background())
		require.Error(t, err)
		assert.ElementsMatch(t,
			[]string{"error1", "error2", "error3", "closererror1", "closererror2"},
			strings.Split(err.Error(), "\n"),
		)
		assert.Equal(t, int32(5), i.Load())
	})

	t.Run("a manger started twice should error", func(t *testing.T) {
		t.Parallel()

		var i atomic.Int32
		m := NewRunnerCloserManager(nil, func(ctx context.Context) error {
			i.Add(1)
			return nil
		})
		assert.NoError(t, m.Run(context.Background()))
		assert.Equal(t, int32(1), i.Load())
		assert.Error(t, m.Run(context.Background()), errors.New("manager already started"))
		assert.Equal(t, int32(1), i.Load())
	})

	t.Run("a manger started twice should error", func(t *testing.T) {
		t.Parallel()

		var i atomic.Int32
		m := NewRunnerCloserManager(nil, func(ctx context.Context) error {
			i.Add(1)
			return nil
		})

		assert.NoError(t, m.AddCloser(func(ctx context.Context) error {
			i.Add(1)
			return nil
		}))

		assert.NoError(t, m.Run(context.Background()))
		assert.Equal(t, int32(2), i.Load())
		assert.NoError(t, m.Close())
		assert.NoError(t, m.Close())
		assert.Error(t, m.Run(context.Background()), errors.New("manager already started"))
		assert.Equal(t, int32(2), i.Load())
	})

	t.Run("adding a task to a started manager should error", func(t *testing.T) {
		t.Parallel()

		var i atomic.Int32
		m := NewRunnerCloserManager(nil, func(ctx context.Context) error {
			i.Add(1)
			return nil
		})
		assert.NoError(t, m.Run(context.Background()))
		assert.Equal(t, int32(1), i.Load())
		err := m.Add(func(ctx context.Context) error {
			i.Add(1)
			return nil
		})
		require.Error(t, err)
		assert.Equal(t, err, errors.New("runner manager already started"))
		assert.Equal(t, int32(1), i.Load())
	})

	t.Run("adding a closer to a closing manager should error", func(t *testing.T) {
		t.Parallel()

		var i atomic.Int32
		m := NewRunnerCloserManager(nil, func(ctx context.Context) error {
			i.Add(1)
			return nil
		})
		assert.NoError(t, m.Run(context.Background()))
		assert.Equal(t, int32(1), i.Load())
		assert.NoError(t, m.Close())
		err := m.AddCloser(func(ctx context.Context) error {
			i.Add(1)
			return nil
		})
		require.Error(t, err)
		assert.Equal(t, err, errors.New("runner manager already closed"))
		assert.Equal(t, int32(1), i.Load())
	})

	t.Run("if grace period is not given, should have no force shutdown", func(t *testing.T) {
		t.Parallel()

		mngr := NewRunnerCloserManager(nil)
		assert.Len(t, mngr.closers, 0)
	})

	t.Run("if grace period is given, should have force shutdown", func(t *testing.T) {
		t.Parallel()

		dur := time.Second
		mngr := NewRunnerCloserManager(&dur)
		assert.Len(t, mngr.closers, 1)
	})

	t.Run("if closing but grace period not reached, should return", func(t *testing.T) {
		t.Parallel()

		dur := time.Second
		mngr := NewRunnerCloserManager(&dur)

		var i atomic.Int32
		assert.NoError(t, mngr.AddCloser(func() {
			i.Add(1)
		}))

		assert.Len(t, mngr.closers, 2)

		clock := clocktesting.NewFakeClock(time.Now())
		mngr.clock = clock

		fatalCalled := make(chan struct{})
		mngr.WithFatalShutdown(func() {
			i.Add(1)
			close(fatalCalled)
		})

		errCh := make(chan error)
		go func() {
			errCh <- mngr.Run(context.Background())
		}()

		select {
		case err := <-errCh:
			assert.NoError(t, err)
		case <-fatalCalled:
			t.Error("fatal shutdown called")
		case <-time.After(time.Second * 3):
			t.Error("Run() not returned")
		}

		assert.Eventually(t, func() bool {
			return !clock.HasWaiters()
		}, time.Second*3, time.Millisecond*100,
			"fatal shutdown should have not have been called and returned",
		)

		assert.Equal(t, int32(1), i.Load())
	})

	t.Run("if closing and grace period is reached, should force shutdown", func(t *testing.T) {
		t.Parallel()

		dur := time.Second
		mngr := NewRunnerCloserManager(&dur)
		assert.Len(t, mngr.closers, 1)

		clock := clocktesting.NewFakeClock(time.Now())
		mngr.clock = clock

		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)

		fatalCalled := make(chan struct{})
		mngr.WithFatalShutdown(func() {
			close(fatalCalled)
		})
		mngr.AddCloser(func() error {
			<-ctx.Done()
			return nil
		})

		errCh := make(chan error)
		t.Cleanup(func() {
			select {
			case err := <-errCh:
				assert.NoError(t, err)
			case <-time.After(time.Second * 3):
				t.Error("manager not closed")
			}
		})
		go func() {
			errCh <- mngr.Run(context.Background())
		}()

		assert.Eventually(t, func() bool {
			return clock.HasWaiters()
		}, time.Second*3, time.Millisecond*100)

		clock.Step(time.Second * 2)

		select {
		case <-fatalCalled:
		case <-time.After(time.Second * 3):
			t.Error("fatal shutdown not called")
		}
		cancel()
	})
}

func TestClose(t *testing.T) {
	t.Parallel()

	t.Run("calling close should stop the main runner and call all closers", func(t *testing.T) {
		t.Parallel()

		var i atomic.Int32
		runnerWaiting := make(chan struct{})
		mngr := NewRunnerCloserManager(nil, func(ctx context.Context) error {
			close(runnerWaiting)
			<-ctx.Done()
			i.Add(1)
			return nil
		})
		assert.NoError(t, mngr.AddCloser(func() {
			i.Add(1)
		}))

		errCh := make(chan error)
		go func() {
			errCh <- mngr.Run(context.Background())
		}()

		select {
		case <-runnerWaiting:
		case <-time.After(time.Second * 3):
			t.Error("runner not waiting")
		}

		assert.NoError(t, mngr.Close())

		select {
		case err := <-errCh:
			assert.NoError(t, err)
		case <-time.After(time.Second * 3):
			t.Error("Run() not returned")
		}

		assert.Equal(t, int32(2), i.Load())
	})

	t.Run("calling close should wait for all closers to return", func(t *testing.T) {
		t.Parallel()

		var i atomic.Int32
		runnerWaiting := make(chan struct{})
		mngr := NewRunnerCloserManager(nil, func(ctx context.Context) error {
			close(runnerWaiting)
			<-ctx.Done()
			i.Add(1)
			return nil
		})

		returnClose := make(chan struct{})
		assert.NoError(t, mngr.AddCloser(
			func() {
				i.Add(1)
				<-returnClose
			},
			func() error {
				i.Add(1)
				<-returnClose
				return nil
			},
			func(context.Context) error {
				i.Add(1)
				<-returnClose
				return nil
			},
			mockCloser(func() error {
				i.Add(1)
				<-returnClose
				return nil
			}),
		))

		assert.Len(t, mngr.closers, 4)

		errCh := make(chan error)
		go func() {
			errCh <- mngr.Run(context.Background())
		}()

		select {
		case <-runnerWaiting:
		case <-time.After(time.Second * 3):
			t.Error("runner not waiting")
		}

		// Should be zero because main runner context is not cancelled yet.
		assert.Equal(t, int32(0), i.Load())

		closeReturned := make(chan struct{})
		go func() {
			mngr.Close()
			close(closeReturned)
		}()

		assert.Eventually(t, func() bool {
			return i.Load() == 5
		}, time.Second*3, time.Millisecond*100)

		close(returnClose)

		select {
		case err := <-errCh:
			assert.NoError(t, err)
		case <-time.After(time.Second * 3):
			t.Error("Run() not returned")
		}

		select {
		case <-closeReturned:
		case <-time.After(time.Second * 3):
			t.Error("Close() not returned")
		}
	})

	t.Run("calling close should wait for all closers return but should not call fatal when enabled", func(t *testing.T) {
		t.Parallel()

		dur := time.Second
		var i atomic.Int32
		runnerWaiting := make(chan struct{})
		mngr := NewRunnerCloserManager(&dur, func(ctx context.Context) error {
			close(runnerWaiting)
			<-ctx.Done()
			i.Add(1)
			return nil
		})

		clock := clocktesting.NewFakeClock(time.Now())
		mngr.clock = clock

		mngr.WithFatalShutdown(func() {
			i.Add(1)
		})

		assert.Len(t, mngr.closers, 1)

		returnClose := make(chan struct{})
		assert.NoError(t, mngr.AddCloser(
			func() {
				i.Add(1)
				<-returnClose
			},
			func() error {
				i.Add(1)
				<-returnClose
				return nil
			},
			func(context.Context) error {
				i.Add(1)
				<-returnClose
				return nil
			},
			mockCloser(func() error {
				i.Add(1)
				<-returnClose
				return nil
			}),
		))

		assert.Len(t, mngr.closers, 5)

		errCh := make(chan error)
		go func() {
			errCh <- mngr.Run(context.Background())
		}()

		select {
		case <-runnerWaiting:
		case <-time.After(time.Second * 3):
			t.Error("runner not waiting")
		}

		// Should be zero because main runner context is not cancelled yet.
		assert.Equal(t, int32(0), i.Load())

		closeReturned := make(chan struct{})
		go func() {
			mngr.Close()
			close(closeReturned)
		}()

		assert.Eventually(t, func() bool {
			return i.Load() == 5
		}, time.Second*3, time.Millisecond*100)

		close(returnClose)

		select {
		case err := <-errCh:
			assert.NoError(t, err)
		case <-time.After(time.Second * 3):
			t.Error("Run() not returned")
		}

		select {
		case <-closeReturned:
		case <-time.After(time.Second * 3):
			t.Error("Close() not returned")
		}

		assert.Equal(t, int32(5), i.Load())
	})

	t.Run("calling close should call fatal if the grace period is reached", func(t *testing.T) {
		t.Parallel()

		dur := time.Second
		var i atomic.Int32
		runnerWaiting := make(chan struct{})
		mngr := NewRunnerCloserManager(&dur, func(ctx context.Context) error {
			close(runnerWaiting)
			<-ctx.Done()
			i.Add(1)
			return nil
		})

		clock := clocktesting.NewFakeClock(time.Now())
		mngr.clock = clock

		fatalCalled := make(chan struct{})
		mngr.WithFatalShutdown(func() {
			close(fatalCalled)
		})

		assert.Len(t, mngr.closers, 1)

		returnClose := make(chan struct{})
		for n := 0; n < 4; n++ {
			assert.NoError(t, mngr.AddCloser(func() {
				i.Add(1)
				<-returnClose
			}))
		}

		assert.Len(t, mngr.closers, 5)

		errCh := make(chan error)
		go func() {
			errCh <- mngr.Run(context.Background())
		}()

		select {
		case <-runnerWaiting:
		case <-time.After(time.Second * 3):
			t.Error("runner not waiting")
		}

		// Should be zero because main runner context is not cancelled yet.
		assert.Equal(t, int32(0), i.Load())

		closeReturned := make(chan struct{})
		go func() {
			mngr.Close()
			close(closeReturned)
		}()

		// Wait for all closers to be called, and fatal routine is waiting.
		assert.Eventually(t, func() bool {
			return clock.HasWaiters() && i.Load() == 5
		}, time.Second*3, time.Millisecond*100)

		clock.Step(time.Second)

		select {
		case <-fatalCalled:
		case <-closeReturned:
			t.Error("Close() returned")
		case <-time.After(time.Second * 3):
			t.Error("fatal not called")
		}

		close(returnClose)
	})

	t.Run("calling close should return the errors from the main runner and all closers", func(t *testing.T) {
		t.Parallel()

		mngr := NewRunnerCloserManager(nil,
			func(ctx context.Context) error {
				return errors.New("error1")
			},
			func(ctx context.Context) error {
				return errors.New("error2")
			},
			func(ctx context.Context) error {
				return errors.New("error3")
			},
		)

		assert.NoError(t, mngr.AddCloser(
			func() error {
				return errors.New("error4")
			},
			func(context.Context) error {
				return errors.New("error5")
			},
			mockCloser(func() error {
				return errors.New("error6")
			}),
		))

		errCh := make(chan error)
		go func() {
			errCh <- mngr.Run(context.Background())
		}()

		var err error
		select {
		case err = <-errCh:
		case <-time.After(time.Second * 3):
			t.Error("Run() not returned")
		}

		exp := []string{"error1", "error2", "error3", "error4", "error5", "error6"}
		assert.ElementsMatch(t, exp, strings.Split(err.Error(), "\n"))
		assert.ElementsMatch(t, exp, strings.Split(mngr.Close().Error(), "\n"))
		assert.ElementsMatch(t, exp, strings.Split(mngr.Close().Error(), "\n"))
	})

	t.Run("calling Close before Run should return immediately", func(t *testing.T) {
		dur := time.Second
		mngr := NewRunnerCloserManager(&dur,
			func(ctx context.Context) error {
				return errors.New("error1")
			},
		)
		assert.NoError(t, mngr.AddCloser(func() error {
			return errors.New("error2")
		}))

		assert.NoError(t, mngr.Close())
		assert.NoError(t, mngr.Close())
		assert.Equal(t, mngr.Run(context.Background()), errors.New("runner manager already started"))
	})
}

func TestAddCloser(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		closers []any
		expErr  error
	}{
		"Add supported closer type": {
			closers: []any{func() {}},
		},
		"Add unsupported closer type": {
			closers: []any{42},
			expErr:  errors.Join(errors.New("unsupported closer type: int")),
		},
		"Add various supported closer types": {
			closers: []any{new(mockCloser), func(ctx context.Context) error { return nil }, func() error { return nil }, func() {}},
			expErr:  nil,
		},
		"Add combination of supported and unsupported closer types": {
			closers: []any{new(mockCloser), 42},
			expErr:  errors.Join(errors.New("unsupported closer type: int")),
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			err := NewRunnerCloserManager(nil).AddCloser(test.closers...)
			assert.Equalf(t, test.expErr, err, "%v", err)
		})
	}

	t.Run("no error if adding a closer during main routine", func(t *testing.T) {
		t.Parallel()

		mngr := NewRunnerCloserManager(nil, func(ctx context.Context) error {
			<-ctx.Done()
			return nil
		})

		ctx, cancel := context.WithCancel(context.Background())
		errCh := make(chan error)
		go func() {
			errCh <- mngr.Run(ctx)
		}()
		assert.NoError(t, mngr.AddCloser(func() {}))
		cancel()
		assert.NoError(t, <-errCh)
	})

	t.Run("should error if closing", func(t *testing.T) {
		t.Parallel()

		mngr := NewRunnerCloserManager(nil)

		ctx, cancel := context.WithCancel(context.Background())
		closerCh := make(chan struct{})
		assert.NoError(t, mngr.AddCloser(func() {
			cancel()
			<-closerCh
		}))

		errCh := make(chan error)
		go func() {
			errCh <- mngr.Run(context.Background())
		}()

		select {
		case <-ctx.Done():
		case <-time.After(time.Second * 3):
			t.Error("Run() not returned")
		}

		closeErrCh := make(chan error)
		go func() {
			closeErrCh <- mngr.AddCloser(nil)
		}()

		close(closerCh)

		select {
		case err := <-errCh:
			assert.NoError(t, err)
		case <-time.After(time.Second * 3):
			t.Error("Run() not returned")
		}

		select {
		case err := <-closeErrCh:
			assert.Equal(t, err, errors.New("runner manager already closed"))
		case <-time.After(time.Second * 3):
			t.Error("AddCloser() not returned")
		}
	})

	t.Run("should error if manager already returned", func(t *testing.T) {
		t.Parallel()

		mngr := NewRunnerCloserManager(nil)
		assert.NoError(t, mngr.Run(context.Background()))
		assert.Equal(t, mngr.AddCloser(nil), errors.New("runner manager already closed"))
	})
}

func TestWaitUntilShutdown(t *testing.T) {
	t.Parallel()

	dur := time.Second * 3
	mngr := NewRunnerCloserManager(&dur, func(ctx context.Context) error {
		<-ctx.Done()
		return nil
	})

	clock := clocktesting.NewFakeClock(time.Now())
	mngr.clock = clock

	shutDownReturned := make(chan struct{})
	go func() {
		mngr.WaitUntilShutdown()
		close(shutDownReturned)
	}()

	returnClose := make(chan struct{})
	assert.NoError(t, mngr.AddCloser(func() {
		<-returnClose
	}))

	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error)
	go func() {
		errCh <- mngr.Run(ctx)
	}()

	cancel()

	select {
	case <-shutDownReturned:
		t.Error("WaitUntilShutdown() returned")
	case <-errCh:
		t.Error("Run() returned")
	default:
	}

	assert.Eventually(t, func() bool {
		return clock.HasWaiters()
	}, time.Second*3, time.Millisecond*100, "fatal shutdown should be waiting")

	select {
	case <-shutDownReturned:
		t.Error("WaitUntilShutdown() returned")
	case <-errCh:
		t.Error("Run() returned")
	default:
	}

	close(returnClose)

	select {
	case <-shutDownReturned:
	case <-time.After(time.Second * 3):
		t.Error("WaitUntilShutdown() not returned")
	}

	select {
	case err := <-errCh:
		assert.NoError(t, err)
	case <-time.After(time.Second * 3):
		t.Error("Run() not returned")
	}
}
