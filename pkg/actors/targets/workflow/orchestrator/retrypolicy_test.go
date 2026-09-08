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
	"github.com/dapr/durabletask-go/api/protos"
)

// assertJitteredRetryForever asserts the reminder carries the shared
// jittered retry-forever policy: constant (the scheduler protocol can only
// express a constant per-job interval), unbounded retries, and an interval
// drawn from [RetryBackoffBase, RetryBackoffCap) rather than a fixed 1s.
func assertJitteredRetryForever(t *testing.T, req *actorapi.CreateReminderRequest) time.Duration {
	t.Helper()

	constant := req.FailurePolicy.GetConstant()
	require.NotNil(t, constant)
	assert.Nil(t, constant.MaxRetries)

	interval := constant.GetInterval().AsDuration()
	assert.GreaterOrEqual(t, interval, common.RetryBackoffBase)
	assert.Less(t, interval, common.RetryBackoffCap)
	return interval
}

// Test_reminderRetryPoliciesAreJittered pins the retry failure policy on
// every orchestrator-side reminder create path: retention, wake-up (via
// buildReminderRequest), cascade-terminate and durable timer. A constant
// 1s policy retries every failed reminder in the fleet in lockstep; the
// jittered policy decorrelates across jobs.
func Test_reminderRetryPoliciesAreJittered(t *testing.T) {
	t.Parallel()

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

	actors := fake.New().WithReminders(func(context.Context) (reminders.Interface, error) {
		return fakeRems, nil
	})

	fact, err := New(t.Context(), Options{
		AppID:              "testapp",
		Namespace:          "default",
		WorkflowActorType:  "dapr.internal.default.testapp.workflow",
		ActivityActorType:  "dapr.internal.default.testapp.activity",
		RetentionActorType: "dapr.internal.default.testapp.retention",
		ActorTypeBuilder:   common.NewActorTypeBuilder("default"),
		Actors:             actors,
	})
	require.NoError(t, err)

	o := fact.GetOrCreate("policy-test-wf").(*orchestrator)

	_, err = o.createRetentionReminder(t.Context(), "retention", time.Now())
	require.NoError(t, err)

	require.NoError(t, o.createCascadeTerminateReminder(t.Context(),
		childRef{instanceID: "child-1"}, &protos.ExecutionTerminatedEvent{}))

	require.NoError(t, o.createTimerReminder(t.Context(), "timer",
		wrapperspb.String("payload"), time.Now()))

	mu.Lock()
	require.Len(t, creates, 3)
	for _, req := range creates {
		assertJitteredRetryForever(t, req)
	}
	mu.Unlock()

	req, err := o.buildReminderRequest("wake-up", nil, time.Now(),
		o.actorTypeBuilder.Workflow("testapp"), nil)
	require.NoError(t, err)
	assertJitteredRetryForever(t, req)

	// Draws must actually decorrelate across creates: repeated builds of
	// the same reminder must not all pin the same interval.
	seen := make(map[time.Duration]struct{})
	for range 50 {
		req, err := o.buildReminderRequest("wake-up", nil, time.Now(),
			o.actorTypeBuilder.Workflow("testapp"), nil)
		require.NoError(t, err)
		seen[assertJitteredRetryForever(t, req)] = struct{}{}
	}
	assert.Greater(t, len(seen), 1)
}
