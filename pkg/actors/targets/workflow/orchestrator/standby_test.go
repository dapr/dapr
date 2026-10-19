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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/fake"
	"github.com/dapr/durabletask-go/backend"
)

func TestStandby(t *testing.T) {
	newFactory := func(t *testing.T, standby bool) *factory {
		t.Helper()
		fact, err := New(t.Context(), Options{
			AppID:             "appID",
			ActivityActorType: "activity",
			WorkflowActorType: "workflow",
			Scheduler: func(context.Context, *backend.WorkflowWorkItem) error {
				return nil
			},
			Actors:   fake.New(),
			FastPath: true,
			Standby:  standby,
		})
		require.NoError(t, err)
		return fact.(*factory)
	}

	t.Run("a standby host never takes the fast path", func(t *testing.T) {
		assert.True(t, newFactory(t, false).fastPath)
		assert.False(t, newFactory(t, true).fastPath)
	})

	t.Run("a standby host refuses the reminders that run a turn", func(t *testing.T) {
		o := newFactory(t, true).GetOrCreate("wf1").(*orchestrator)
		err := o.InvokeReminder(t.Context(), &api.Reminder{Name: "start-1"})
		require.Error(t, err)
		assert.Equal(t, codes.FailedPrecondition, status.Code(err))
		require.NoError(t, o.Deactivate(t.Context()))
	})
}
