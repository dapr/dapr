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
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/wrapperspb"

	actorapi "github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/fake"
	"github.com/dapr/dapr/pkg/actors/reminders"
	remindersfake "github.com/dapr/dapr/pkg/actors/reminders/fake"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/common"
	"github.com/dapr/dapr/pkg/config"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/durabletask-go/api/protos"
)

func pullTurnOrchestrator(t *testing.T, opts Options) (*orchestrator, func() []*actorapi.CreateReminderRequest) {
	t.Helper()

	var (
		mu      sync.Mutex
		creates []*actorapi.CreateReminderRequest
	)
	fakeRems := remindersfake.New().
		WithCreate(func(_ context.Context, req *actorapi.CreateReminderRequest) error {
			mu.Lock()
			defer mu.Unlock()
			creates = append(creates, req)
			return nil
		})
	opts.AppID = "testapp"
	opts.Namespace = "default"
	opts.WorkflowActorType = "dapr.internal.default.testapp.workflow"
	opts.ActivityActorType = "dapr.internal.default.testapp.activity"
	opts.RetentionActorType = "dapr.internal.default.testapp.retention"
	opts.ActorTypeBuilder = common.NewActorTypeBuilder("default")
	opts.Actors = fake.New().WithReminders(func(context.Context) (reminders.Interface, error) {
		return fakeRems, nil
	})

	fact, err := New(t.Context(), opts)
	require.NoError(t, err)
	return fact.GetOrCreate("pull-turn-wf").(*orchestrator), func() []*actorapi.CreateReminderRequest {
		mu.Lock()
		defer mu.Unlock()
		return creates
	}
}

const paymentsName = "payments"

func workflowPriorityOf(name string) (string, uint32) {
	switch name {
	case paymentsName:
		return config.PriorityLevelCritical, 6
	case "":
		return config.PriorityLevelLow, 2
	}
	return "", 0
}

func Test_pullTurn(t *testing.T) {
	t.Parallel()

	t.Run("turn reminders are pull with the workflow's priority", func(t *testing.T) {
		t.Parallel()
		o, creates := pullTurnOrchestrator(t, Options{WorkflowDispatchPull: true, WorkflowPriority: workflowPriorityOf})
		own := o.actorTypeBuilder.Workflow("testapp")
		payments := paymentsName

		for _, name := range []string{"start-es-1", "new-event-es-1", reminderNameParentNotify} {
			req, err := o.buildReminderRequest(name, nil, time.Now(), own, &payments)
			require.NoError(t, err)
			assert.True(t, req.Pull, name)
			assert.Equal(t, schedulerv1pb.PriorityLevel_PRIORITY_LEVEL_CRITICAL, req.PriorityLevel, name)
			assert.Equal(t, uint32(6), req.FairnessWeight, name)
		}

		// A timer takes its workflow's priority.
		require.NoError(t, o.createTimerReminder(t.Context(), "timer-1", wrapperspb.String("payload"), time.Now(), paymentsName))
		// A cascade terminate takes the child's priority.
		require.NoError(t, o.createCascadeTerminateReminder(t.Context(), childRef{instanceID: "child-1", name: paymentsName}, &protos.ExecutionTerminatedEvent{}))
		// A workflow without an entry takes the default.
		require.NoError(t, o.createTimerReminder(t.Context(), "timer-2", wrapperspb.String("payload"), time.Now(), ""))
		got := creates()
		require.Len(t, got, 3)
		for _, req := range got {
			assert.True(t, req.Pull, req.Name)
		}
		assert.Equal(t, paymentsName, *got[0].ConcurrencyKey, "a pulled timer carries the workflow name")
		assert.Nil(t, got[2].ConcurrencyKey)
		for _, req := range got[:2] {
			assert.Equal(t, schedulerv1pb.PriorityLevel_PRIORITY_LEVEL_CRITICAL, req.PriorityLevel, req.Name)
			assert.Equal(t, uint32(6), req.FairnessWeight, req.Name)
		}
		assert.Equal(t, schedulerv1pb.PriorityLevel_PRIORITY_LEVEL_LOW, got[2].PriorityLevel)
		assert.Equal(t, uint32(2), got[2].FairnessWeight)
	})

	t.Run("reminders that run no turn stay unmarked", func(t *testing.T) {
		t.Parallel()
		o, _ := pullTurnOrchestrator(t, Options{WorkflowDispatchPull: true, WorkflowPriority: workflowPriorityOf})
		own := o.actorTypeBuilder.Workflow("testapp")
		payments := paymentsName

		for _, name := range []string{janitorReminderName, common.ReminderPrefixActivityResult + "-abc"} {
			req, err := o.buildReminderRequest(name, nil, time.Now(), own, &payments)
			require.NoError(t, err)
			assert.False(t, req.Pull, name)
			assert.Equal(t, schedulerv1pb.PriorityLevel_PRIORITY_LEVEL_UNSPECIFIED, req.PriorityLevel, name)
			assert.Zero(t, req.FairnessWeight, name)
		}
	})

	t.Run("another app's workflow keeps its regular routing", func(t *testing.T) {
		t.Parallel()
		o, _ := pullTurnOrchestrator(t, Options{WorkflowDispatchPull: true, WorkflowPriority: workflowPriorityOf})
		payments := paymentsName
		req, err := o.buildReminderRequest("start-es-1", nil, time.Now(), o.actorTypeBuilder.Workflow("otherapp"), &payments)
		require.NoError(t, err)
		assert.False(t, req.Pull)
		assert.Equal(t, schedulerv1pb.PriorityLevel_PRIORITY_LEVEL_UNSPECIFIED, req.PriorityLevel)
	})

	t.Run("off unless configured", func(t *testing.T) {
		t.Parallel()
		o, creates := pullTurnOrchestrator(t, Options{WorkflowPriority: workflowPriorityOf})
		payments := paymentsName
		req, err := o.buildReminderRequest("start-es-1", nil, time.Now(), o.actorTypeBuilder.Workflow("testapp"), &payments)
		require.NoError(t, err)
		assert.False(t, req.Pull)
		assert.Equal(t, schedulerv1pb.PriorityLevel_PRIORITY_LEVEL_UNSPECIFIED, req.PriorityLevel)
		require.NoError(t, o.createTimerReminder(t.Context(), "timer-1", wrapperspb.String("payload"), time.Now(), paymentsName))
		require.Len(t, creates(), 1)
		assert.False(t, creates()[0].Pull)
	})

	t.Run("pull without a priority block leaves priority unset", func(t *testing.T) {
		t.Parallel()
		o, _ := pullTurnOrchestrator(t, Options{WorkflowDispatchPull: true})
		payments := paymentsName
		req, err := o.buildReminderRequest("start-es-1", nil, time.Now(), o.actorTypeBuilder.Workflow("testapp"), &payments)
		require.NoError(t, err)
		assert.True(t, req.Pull)
		assert.Equal(t, schedulerv1pb.PriorityLevel_PRIORITY_LEVEL_UNSPECIFIED, req.PriorityLevel)
		assert.Zero(t, req.FairnessWeight)
	})
}

func Test_isTurnReminder(t *testing.T) {
	t.Parallel()
	for name, want := range map[string]bool{
		"start-es-1":             true,
		"new-event-es-1":         true,
		"timer-3":                true,
		reminderCascadeTerminate: true,
		reminderNameParentNotify: true,
		janitorReminderName:      false,
		common.ReminderPrefixActivityResult + "-x": false,
		"retention": false,
	} {
		assert.Equal(t, want, isTurnReminder(name), name)
	}
}
