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

package cluster

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	actorapi "github.com/dapr/dapr/pkg/actors/api"
	routerfake "github.com/dapr/dapr/pkg/actors/router/fake"
	"github.com/dapr/dapr/pkg/config"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	wfenginefake "github.com/dapr/dapr/pkg/runtime/wfengine/fake"
)

const testWorkflowType = "dapr.internal.default.myapp.workflow"

func closed(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

func TestWorkflowSlots(t *testing.T) {
	t.Parallel()
	wf := newWorkflowSlots(2)

	free, changed := wf.free()
	assert.Equal(t, uint32(2), free)
	require.True(t, wf.reserve("a"))
	assert.True(t, closed(changed), "a reserve is a change")

	require.True(t, wf.reserve("b"))
	free, changed = wf.free()
	assert.Equal(t, uint32(0), free)
	assert.False(t, wf.reserve("c"), "no slot is free")
	assert.False(t, closed(changed), "a refusal changes nothing")

	wf.release("a")
	assert.True(t, closed(changed), "a release is a change")
	free, _ = wf.free()
	assert.Equal(t, uint32(1), free)
}

func TestWorkflowSlots_OneSlotPerWorkflow(t *testing.T) {
	t.Parallel()
	wf := newWorkflowSlots(1)
	require.True(t, wf.reserve("wf-1"))
	require.True(t, wf.reserve("wf-1"), "another turn of the same workflow shares its slot")
	assert.False(t, wf.reserve("wf-2"))

	wf.release("wf-1")
	free, _ := wf.free()
	assert.Equal(t, uint32(0), free, "the other turn still runs")
	wf.release("wf-1")
	free, _ = wf.free()
	assert.Equal(t, uint32(1), free)
}

func TestPull_WorkflowSlotsOutliveClusters(t *testing.T) {
	t.Parallel()
	p := NewPull(t.Context())
	wf := p.workflowSlots(1)
	require.True(t, wf.reserve("a"))
	assert.Same(t, wf, p.workflowSlots(1), "a new cluster sees the turns held under the last one")
	free, _ := p.workflowSlots(1).free()
	assert.Equal(t, uint32(0), free)
}

func TestCluster_PullTypes(t *testing.T) {
	t.Parallel()
	email := "SendEmail"
	pull := new(config.DispatchModePull)
	for name, tc := range map[string]struct {
		spec      *config.WorkflowSpec
		supported bool
		noPull    bool
		want      map[string]bool
	}{
		"activities only": {
			spec:      &config.WorkflowSpec{DispatchMode: pull, MaxConcurrentActivityInvocations: 3},
			supported: true,
			want:      map[string]bool{testActivityType: true},
		},
		"activities and workflow turns": {
			spec:      &config.WorkflowSpec{DispatchMode: pull, MaxConcurrentActivityInvocations: 3, MaxConcurrentWorkflowInvocations: 2},
			supported: true,
			want:      map[string]bool{testActivityType: true, testWorkflowType: false},
		},
		"a per-name activity pull leaves workflow turns alone": {
			spec: &config.WorkflowSpec{
				MaxConcurrentActivityInvocations: 3,
				MaxConcurrentWorkflowInvocations: 2,
				ActivityConcurrencyLimits:        []config.ActivityConcurrencyLimit{{Name: &email, DispatchMode: pull}},
			},
			supported: true,
			want:      map[string]bool{testActivityType: true},
		},
		"hashed": {
			spec:      &config.WorkflowSpec{MaxConcurrentActivityInvocations: 3, MaxConcurrentWorkflowInvocations: 2},
			supported: true,
			want:      map[string]bool{},
		},
		"a scheduler without pull support": {
			spec: &config.WorkflowSpec{DispatchMode: pull, MaxConcurrentActivityInvocations: 3, MaxConcurrentWorkflowInvocations: 2},
		},
		"no pull state": {
			spec:      &config.WorkflowSpec{DispatchMode: pull, MaxConcurrentActivityInvocations: 3, MaxConcurrentWorkflowInvocations: 2},
			supported: true,
			noPull:    true,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			c := &Cluster{
				workflowSpec:      tc.spec,
				wfengine:          wfenginefake.New().WithActorTypes(testWorkflowType, testActivityType),
				supportsPullSlots: tc.supported,
			}
			if !tc.noPull {
				c.pull = NewPull(t.Context())
			}
			types := c.pullTypes()
			got := make(map[string]bool, len(types))
			for actorType, pt := range types {
				got[actorType] = pt.local()
				assert.Equal(t, actorType == testWorkflowType, pt.workflow != nil, actorType)
			}
			if tc.want == nil {
				assert.Empty(t, types)
				return
			}
			assert.Equal(t, tc.want, got)
			if pt, ok := types[testWorkflowType]; ok {
				free, _ := pt.slots()
				assert.Equal(t, uint32(2), free, "maxConcurrentWorkflowInvocations slots")
			}
		})
	}
}

func turnJob(name string) *schedulerv1pb.WatchJobsResponse {
	return &schedulerv1pb.WatchJobsResponse{
		Name: name,
		Pull: new(true),
		Metadata: &schedulerv1pb.JobMetadata{
			Pull: new(true),
			Target: &schedulerv1pb.JobTargetMetadata{
				Type: &schedulerv1pb.JobTargetMetadata_Actor{
					Actor: &schedulerv1pb.TargetActorReminder{Type: testWorkflowType, Id: "wf-1"},
				},
			},
		},
	}
}

func TestStreamer_WorkflowTurnHoldsASlot(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	started := make(chan *actorapi.Reminder, 2)
	var calls atomic.Int32
	actors := routerfake.New().WithCallReminderFn(func(_ context.Context, rem *actorapi.Reminder) error {
		calls.Add(1)
		started <- rem
		<-release
		return nil
	})
	wf := newWorkflowSlots(1)
	s := &streamer{
		actors:    actors,
		wfengine:  wfenginefake.New().WithActorTypes(testWorkflowType, testActivityType),
		pull:      NewPull(t.Context()),
		pullTypes: map[string]pullType{testWorkflowType: {workflow: wf}},
	}
	pt, ok := s.pullTypeOf(turnJob("new-event-1"))
	require.True(t, ok)
	other := turnJob("new-event-1")
	other.Metadata.Target.Type.(*schedulerv1pb.JobTargetMetadata_Actor).Actor.Id = "wf-2"

	first := make(chan schedulerv1pb.WatchJobsRequestResultStatus, 1)
	go func() { first <- s.handlePullJob(t.Context(), turnJob("new-event-1"), pt) }()
	rem := <-started
	assert.False(t, rem.ExecuteLocally, "a turn goes to the placement owner")
	assert.False(t, rem.SkipLock, "a turn takes the actor lock")
	free, _ := wf.free()
	assert.Equal(t, uint32(0), free, "the running turn holds the slot")

	assert.Equal(t, schedulerv1pb.WatchJobsRequestResultStatus_NO_CAPACITY, s.handlePullJob(t.Context(), other, pt))
	assert.Equal(t, int32(1), calls.Load(), "a refused turn runs nothing")

	// Another turn of the same workflow shares its slot.
	second := make(chan schedulerv1pb.WatchJobsRequestResultStatus, 1)
	go func() { second <- s.handlePullJob(t.Context(), turnJob("timer-1"), pt) }()
	<-started

	close(release)
	assert.Equal(t, schedulerv1pb.WatchJobsRequestResultStatus_SUCCESS, <-first)
	assert.Equal(t, schedulerv1pb.WatchJobsRequestResultStatus_SUCCESS, <-second)
	free, _ = wf.free()
	assert.Equal(t, uint32(1), free, "a finished turn gives its slot back")
}

func TestStreamer_ReportPullReportsEveryType(t *testing.T) {
	t.Parallel()

	var lock sync.Mutex
	activityChanged := make(chan struct{})
	wf := newWorkflowSlots(2)
	p := NewPull(t.Context())
	s := testStreamer(p, "sched-0")
	s.pullTypes = map[string]pullType{
		testActivityType: {free: func() (uint32, <-chan struct{}) {
			lock.Lock()
			defer lock.Unlock()
			return 3, activityChanged
		}},
		testWorkflowType: {workflow: wf},
	}
	p.attach(s)

	ctx, cancel := context.WithCancel(t.Context())
	errCh := make(chan error, 1)
	go func() { errCh <- s.reportPull(ctx) }()

	expect := func(want map[string]uint32, msg string) {
		t.Helper()
		for _, actorType := range []string{testActivityType, testWorkflowType} {
			free, ok := want[actorType]
			if !ok {
				continue
			}
			got := next(t, s).GetPullSlots()
			assert.Equal(t, actorType, got.GetActorType(), msg)
			assert.Equal(t, free, got.GetFree(), msg)
		}
		assert.Empty(t, s.resultCh, msg)
	}
	expect(map[string]uint32{testActivityType: 3, testWorkflowType: 2}, "every type on start")

	require.True(t, wf.reserve("a"))
	expect(map[string]uint32{testWorkflowType: 1}, "a turn change reports only the workflow type")

	lock.Lock()
	close(activityChanged)
	activityChanged = make(chan struct{})
	lock.Unlock()
	expect(map[string]uint32{testActivityType: 3}, "an activity change reports only the activity type")

	// Both change before the reporter looks: both are sent.
	lock.Lock()
	close(activityChanged)
	activityChanged = make(chan struct{})
	require.True(t, wf.reserve("b"))
	lock.Unlock()
	expect(map[string]uint32{testActivityType: 3, testWorkflowType: 0}, "changes to both types are both sent")

	cancel()
	<-errCh
}

func TestConnector_InitialCarriesEveryType(t *testing.T) {
	t.Parallel()
	wf := newWorkflowSlots(4)
	require.True(t, wf.reserve("a"))
	c := &connector{
		req: &schedulerv1pb.WatchJobsRequest{WatchJobRequestType: &schedulerv1pb.WatchJobsRequest_Initial{
			Initial: &schedulerv1pb.WatchJobsRequestInitial{AppId: "myapp"},
		}},
		addr: "sched-0",
		pull: NewPull(t.Context()),
		pullTypes: map[string]pullType{
			testWorkflowType: {workflow: wf},
			testActivityType: {free: func() (uint32, <-chan struct{}) { return 3, nil }},
		},
	}

	slots := c.initial().GetInitial().GetPullSlots()
	require.Len(t, slots, 2)
	assert.Equal(t, testActivityType, slots[0].GetActorType())
	assert.Equal(t, uint32(3), slots[0].GetFree())
	assert.Equal(t, testWorkflowType, slots[1].GetActorType())
	assert.Equal(t, uint32(3), slots[1].GetFree())
}

func TestStreamer_ReportPullRepeatsEveryTypeUnderChurn(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var lock sync.Mutex
		activityChanged := make(chan struct{})
		wf := newWorkflowSlots(2)
		p := NewPull(t.Context())
		s := testStreamer(p, "sched-0")
		s.pullTypes = map[string]pullType{
			testActivityType: {free: func() (uint32, <-chan struct{}) {
				lock.Lock()
				defer lock.Unlock()
				return 3, activityChanged
			}},
			testWorkflowType: {workflow: wf},
		}
		p.attach(s)

		ctx, cancel := context.WithCancel(t.Context())
		errCh := make(chan error, 1)
		go func() { errCh <- s.reportPull(ctx) }()
		for range 2 {
			<-s.resultCh
		}

		// The workflow type is still repeated at the interval.
		start := time.Now()
		for {
			time.Sleep(time.Second)
			lock.Lock()
			close(activityChanged)
			activityChanged = make(chan struct{})
			lock.Unlock()
			synctest.Wait()
			var sawTurns bool
			for len(s.resultCh) > 0 {
				if (<-s.resultCh).GetPullSlots().GetActorType() == testWorkflowType {
					sawTurns = true
				}
			}
			if sawTurns {
				break
			}
		}
		assert.Equal(t, pullReportInterval, time.Since(start))

		cancel()
		<-errCh
		close(s.done)
	})
}
