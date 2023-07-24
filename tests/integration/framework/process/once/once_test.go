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

package once

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

type mockProcess struct {
	runCalled     int
	cleanupCalled int
}

func (mp *mockProcess) Run(t *testing.T, ctx context.Context) {
	mp.runCalled++
}

func (mp *mockProcess) Cleanup(t *testing.T) {
	mp.cleanupCalled++
}

func (mp *mockProcess) Dispose() error {
	return nil
}

func TestOnce(t *testing.T) {
	mockProc := &mockProcess{}
	onceProc := Wrap(mockProc)

	runFailedCalled := false
	cleanupFailedCalled := false
	onceProc.(*once).failRun = func(*testing.T) {
		runFailedCalled = true
	}
	onceProc.(*once).failCleanup = func(*testing.T) {
		cleanupFailedCalled = true
	}

	t.Run("Run", func(t *testing.T) {
		ctx := context.Background()

		// First call should succeed
		onceProc.Run(t, ctx)
		assert.Equal(t, 1, mockProc.runCalled)
		assert.False(t, runFailedCalled)

		// Second call should fail
		onceProc.Run(t, ctx)
		assert.True(t, runFailedCalled)
	})

	t.Run("Cleanup", func(t *testing.T) {
		// First call should succeed
		onceProc.Cleanup(t)
		assert.Equal(t, 1, mockProc.cleanupCalled)
		assert.False(t, cleanupFailedCalled)

		// Second call should fail
		onceProc.Cleanup(t)
		assert.True(t, cleanupFailedCalled)
	})
}
