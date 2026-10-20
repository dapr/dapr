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

package orchestrator

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/actors/fake"
	"github.com/dapr/durabletask-go/backend"
)

func TestFallback(t *testing.T) {
	newFactory := func(t *testing.T, fastPath bool, fallback func() bool) *factory {
		t.Helper()
		fact, err := New(t.Context(), Options{
			AppID:             "appID",
			ActivityActorType: "activity",
			WorkflowActorType: "workflow",
			Scheduler: func(context.Context, *backend.WorkflowWorkItem) error {
				return nil
			},
			Actors:   fake.New(),
			FastPath: fastPath,
			Fallback: fallback,
		})
		require.NoError(t, err)
		return fact.(*factory)
	}

	t.Run("with the feature off nothing is a fallback host", func(t *testing.T) {
		f := newFactory(t, true, nil)
		assert.False(t, f.isFallback())
		assert.True(t, f.localFastPath())
	})

	t.Run("a fallback host takes no local fast path, and regains it in place", func(t *testing.T) {
		var fallback atomic.Bool
		fallback.Store(true)
		f := newFactory(t, true, fallback.Load)
		assert.True(t, f.isFallback())
		assert.False(t, f.localFastPath())
		// Turns run only with a local executor, and keep the configured
		// fast path.
		assert.True(t, f.fastPath)

		fallback.Store(false)
		assert.False(t, f.isFallback())
		assert.True(t, f.localFastPath())
	})

	t.Run("without the fast path a fallback host changes nothing for it", func(t *testing.T) {
		f := newFactory(t, false, func() bool { return true })
		assert.False(t, f.localFastPath())
	})

	t.Run("a local wake is not armed on a fallback host", func(t *testing.T) {
		f := newFactory(t, true, func() bool { return true })
		o := f.GetOrCreate("wf1").(*orchestrator)
		o.localDrive("start-1", time.Now())
		assert.False(t, o.driveRunning.Load())
		require.NoError(t, o.Deactivate(t.Context()))
	})
}
