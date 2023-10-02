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

package security

import (
	"context"
	"crypto/x509"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spiffe/go-spiffe/v2/bundle/x509bundle"
	"github.com/spiffe/go-spiffe/v2/svid/x509svid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	clocktesting "k8s.io/utils/clock/testing"
)

func TestValidator(t *testing.T) {
	var _ x509svid.Source = &x509source{}
	var _ x509bundle.Source = &x509source{}
}

func TestStartRotation(t *testing.T) {
	t.Run("if context is cancelled, expect return", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		clock := clocktesting.NewFakeClock(time.Now())
		x := &x509source{clock: clock}

		ch := make(chan struct{})
		go func() {
			defer close(ch)
			x.startRotation(ctx, func(context.Context) (*x509.Certificate, error) {
				t.Error("unexpected call")
				return nil, nil
			}, &x509.Certificate{NotBefore: time.Now(), NotAfter: time.Now().Add(time.Hour)})
		}()

		select {
		case <-time.After(time.Second):
			t.Fatal("expected return")
		case <-ch:
		}
	})

	t.Run("if expiry is in the past, expect renewal to be called", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		clock := clocktesting.NewFakeClock(time.Now())
		x := &x509source{clock: clock}

		ch, renewCalled := make(chan struct{}), make(chan struct{})
		go func() {
			defer close(ch)
			x.startRotation(ctx, func(context.Context) (*x509.Certificate, error) {
				close(renewCalled)
				return &x509.Certificate{NotBefore: clock.Now(), NotAfter: clock.Now().Add(time.Hour)}, nil
			}, &x509.Certificate{NotBefore: clock.Now().Add(-2 * time.Hour), NotAfter: clock.Now().Add(-time.Hour)})
		}()

		assert.Eventually(t, clock.HasWaiters, time.Second, time.Millisecond)
		// Step 0 triggers waiters but doesn't move the time forward.
		clock.Step(0)

		select {
		case <-time.After(time.Second):
			t.Fatal("expected renewal to be called")
		case <-renewCalled:
			cancel()
		}

		select {
		case <-time.After(time.Second):
			t.Fatal("expected return")
		case <-ch:
		}
	})

	t.Run("if expiry is 1 second in the future, expect renew to be called before 3 seconds", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		clock := clocktesting.NewFakeClock(time.Now())
		x := &x509source{clock: clock}

		ch, renewCalled := make(chan struct{}), make(chan struct{})
		go func() {
			defer close(ch)
			x.startRotation(ctx, func(context.Context) (*x509.Certificate, error) {
				close(renewCalled)
				return &x509.Certificate{NotBefore: clock.Now(), NotAfter: clock.Now().Add(time.Hour)}, nil
			}, &x509.Certificate{NotBefore: clock.Now(), NotAfter: clock.Now().Add(time.Second)})
		}()

		assert.Eventually(t, clock.HasWaiters, time.Second, time.Millisecond)
		clock.Step(time.Second)

		select {
		case <-time.After(3 * time.Second):
			t.Fatal("expected renewal to be called")
		case <-renewCalled:
			cancel()
		}

		select {
		case <-time.After(time.Second):
			t.Fatal("expected return")
		case <-ch:
		}
	})

	t.Run("if expiry is 5 seconds in the future, don't expect renew to be called for first 2 seconds", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		clock := clocktesting.NewFakeClock(time.Now())
		x := &x509source{clock: clock}

		ch := make(chan struct{})
		go func() {
			defer close(ch)
			x.startRotation(ctx, func(context.Context) (*x509.Certificate, error) {
				assert.Fail(t, "unexpected renewal to be called")
				return nil, nil
			}, &x509.Certificate{NotBefore: clock.Now(), NotAfter: clock.Now().Add(time.Second * 5)})
		}()

		clock.Step(time.Second * 2)
		cancel()

		select {
		case <-time.After(time.Second):
			t.Fatal("expected return")
		case <-ch:
		}
	})

	t.Run("renewed certificate should be renewed again when appropriate", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		clock := clocktesting.NewFakeClock(time.Now())
		x := &x509source{clock: clock}

		ch := make(chan struct{})

		var i atomic.Int32
		go func() {
			defer close(ch)
			x.startRotation(ctx, func(context.Context) (*x509.Certificate, error) {
				i.Add(1)
				return &x509.Certificate{NotBefore: clock.Now(), NotAfter: clock.Now().Add(time.Second / 4)}, nil
			}, &x509.Certificate{NotBefore: clock.Now(), NotAfter: clock.Now().Add(time.Second / 4)})
		}()

		assert.Eventually(t, clock.HasWaiters, time.Second, time.Millisecond)
		require.Eventually(t, func() bool {
			clock.Step(time.Second / 4)
			return i.Load() == 3
		}, time.Second, time.Millisecond)
		cancel()

		select {
		case <-time.After(time.Second):
			t.Fatal("expected return")
		case <-ch:
		}
	})
}

func Test_atomicWrite(t *testing.T) {
	clock := clocktesting.NewFakeClock(time.Date(2023, time.April, 5, 1, 2, 3, 4, time.UTC))

	tests := map[string]struct {
		before func(t *testing.T, dir string)
		after  func(t *testing.T, dir string)
		data   map[string][]byte
	}{
		"writing to non existing path": {
			data: map[string][]byte{"file1.txt": []byte("hello"), "file2.txt": []byte("world")},
		},
		"writing to a path that already exists": {
			data: map[string][]byte{"file1.txt": []byte("hello"), "file2.txt": []byte("world")},
			before: func(t *testing.T, dir string) {
				alreadyExists := dir + "-foo"
				require.NoError(t, os.MkdirAll(alreadyExists, 0o755))
				os.WriteFile(filepath.Join(alreadyExists, "file1.txt"), []byte("not-hello"), 0o600)
				os.WriteFile(filepath.Join(alreadyExists, "file2.txt"), []byte("not-world"), 0o600)
				require.NoError(t, os.Symlink(alreadyExists, dir))
			},
			after: func(t *testing.T, dir string) {
				// The old directory of data should remain untouched.
				alreadyExists := dir + "-foo"
				stat, err := os.Stat(alreadyExists)
				require.NoError(t, err)
				assert.True(t, stat.IsDir())
				b, err := os.ReadFile(filepath.Join(alreadyExists, "file1.txt"))
				require.NoError(t, err)
				assert.Equal(t, "not-hello", string(b))
				b, err = os.ReadFile(filepath.Join(alreadyExists, "file2.txt"))
				require.NoError(t, err)
				assert.Equal(t, "not-world", string(b))
			},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			tDir := t.TempDir()
			dir := filepath.Join(tDir, "my-dir")
			if test.before != nil {
				test.before(t, dir)
			}

			require.NoError(t, atomicWrite(clock, dir, test.data))

			newDir := filepath.Join(tDir, "my-dir-20230405-010203")
			f, err := os.Stat(newDir)
			require.NoError(t, err)
			assert.True(t, f.IsDir())

			f, err = os.Lstat(dir)
			require.NoError(t, err)
			if runtime.GOOS == "windows" {
				assert.Equal(t, os.ModeDir.Type().String(), f.Mode().Type().String())
			} else {
				assert.Equal(t, os.ModeSymlink.Type().String(), f.Mode().Type().String())
				target, lerr := os.Readlink(dir)
				require.NoError(t, lerr)
				assert.Equal(t, newDir, target)
			}

			walkDir, err := os.ReadDir(newDir)
			require.NoError(t, err)
			assert.Len(t, walkDir, len(test.data))
			for _, f := range walkDir {
				fileData, err := os.ReadFile(filepath.Join(newDir, f.Name()))
				require.NoError(t, err)
				assert.Equal(t, test.data[f.Name()], fileData)
			}

			if test.after != nil {
				test.after(t, dir)
			}
		})
	}
}

func Test_renewalTime(t *testing.T) {
	now := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	tests := map[string]struct {
		notBefore time.Time
		notAfter  time.Time
		expected  time.Time
	}{
		"not before is in the future": {
			notBefore: now.Add(time.Hour),
			notAfter:  now.Add(2 * time.Hour),
			expected:  now.Add(time.Hour * 17 / 10),
		},
		"not before is in the past": {
			notBefore: now.Add(-time.Hour),
			notAfter:  now.Add(time.Hour),
			expected:  now.Add(time.Hour * 4 / 10),
		},
		"not before is now": {
			notBefore: now,
			notAfter:  now.Add(time.Hour),
			expected:  now.Add(time.Hour * 7 / 10),
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			actual := renewalTime(test.notBefore, test.notAfter)
			assert.Equal(t, test.expected, actual)
		})
	}
}

func Test_isControlPlaneService(t *testing.T) {
	tests := map[string]struct {
		name string
		exp  bool
	}{
		"operator should be control plane service": {
			name: "dapr-operator",
			exp:  true,
		},
		"sentry should be control plane service": {
			name: "dapr-sentry",
			exp:  true,
		},
		"placement should be control plane service": {
			name: "dapr-placement",
			exp:  true,
		},
		"sidecar injector should be control plane service": {
			name: "dapr-injector",
			exp:  true,
		},
		"not a control plane service": {
			name: "my-app",
			exp:  false,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, test.exp, isControlPlaneService(test.name))
		})
	}
}
