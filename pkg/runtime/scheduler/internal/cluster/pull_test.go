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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	wfenginefake "github.com/dapr/dapr/pkg/runtime/wfengine/fake"
)

const testActivityType = "dapr.internal.default.myapp.activity"

// testStreamer returns a streamer whose stand-in for outgoing sends every
// message into resultCh until done is closed. sendOK says whether a send
// succeeds; a failed one closes the stream.
func testStreamer(p *Pull, addr string) *streamer {
	return testStreamerSending(p, addr, true)
}

func testStreamerSending(p *Pull, addr string, sendOK bool) *streamer {
	s := &streamer{
		addr:          addr,
		pull:          p,
		pullActorType: testActivityType,
		resultCh:      make(chan *schedulerv1pb.WatchJobsRequest, 16),
		pullCh:        make(chan pullSend),
		done:          make(chan struct{}),
		wfengine:      wfenginefake.New(),
	}
	go func() {
		for {
			select {
			case m := <-s.pullCh:
				if !sendOK {
					m.sent <- false
					close(s.done)
					return
				}
				s.resultCh <- m.req
				m.sent <- true
			case <-s.done:
				return
			}
		}
	}()
	return s
}

func actorTarget(id string) *schedulerv1pb.TargetActorReminder {
	return &schedulerv1pb.TargetActorReminder{Type: testActivityType, Id: id}
}

func wantRef(id string) *schedulerv1pb.ActorReminderRef {
	return &schedulerv1pb.ActorReminderRef{ActorType: testActivityType, ActorId: id, Name: "run-activity"}
}

func next(t *testing.T, s *streamer) *schedulerv1pb.WatchJobsRequest {
	t.Helper()
	select {
	case msg := <-s.resultCh:
		return msg
	case <-time.After(5 * time.Second):
		require.Fail(t, "no message sent")
		return nil
	}
}

// runBlocked starts a pull reminder on s that finishes with status when
// release is closed.
func runBlocked(p *Pull, s *streamer, id uint64, actorID string, status schedulerv1pb.WatchJobsRequestResultStatus) chan struct{} {
	release := make(chan struct{})
	p.run(s, id, actorTarget(actorID), "run-activity", func(context.Context) schedulerv1pb.WatchJobsRequestResultStatus {
		<-release
		return status
	})
	return release
}

func waitHeld(t *testing.T, p *Pull) {
	t.Helper()
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		p.lock.Lock()
		defer p.lock.Unlock()
		for _, job := range p.jobs {
			assert.NotNil(c, job.status)
		}
	}, 5*time.Second, time.Millisecond)
}

func TestPull_ResultByIDOnTheDeliveringStream(t *testing.T) {
	t.Parallel()
	p := NewPull(t.Context())
	s := testStreamer(p, "sched-0")
	p.attach(s)

	release := runBlocked(p, s, 7, "wf::1::0", schedulerv1pb.WatchJobsRequestResultStatus_SUCCESS)
	assert.Equal(t, []*schedulerv1pb.ActorReminderRef{wantRef("wf::1::0")}, p.refs("sched-0"))
	assert.Empty(t, p.refs("sched-1"), "reminders are listed only to the scheduler that delivered them")

	close(release)
	got := next(t, s).GetResult()
	assert.Equal(t, uint64(7), got.GetId())
	assert.Nil(t, got.GetReminder())
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Empty(c, p.refs("sched-0"))
	}, 5*time.Second, time.Millisecond)
}

func TestPull_ResultByReferenceOnAnotherStream(t *testing.T) {
	t.Parallel()
	p := NewPull(t.Context())
	delivering := testStreamer(p, "sched-0")
	other := testStreamer(p, "sched-0")
	elsewhere := testStreamer(p, "sched-1")
	p.attach(delivering)
	p.attach(other)
	p.attach(elsewhere)

	release := runBlocked(p, delivering, 7, "wf::1::0", schedulerv1pb.WatchJobsRequestResultStatus_FAILED)
	close(delivering.done)
	p.detach(delivering)

	close(release)
	got := next(t, other).GetResult()
	assert.Equal(t, uint64(0), got.GetId())
	assert.True(t, proto.Equal(wantRef("wf::1::0"), got.GetReminder()))
	assert.Equal(t, schedulerv1pb.WatchJobsRequestResultStatus_FAILED, got.GetStatus())
	assert.Empty(t, elsewhere.resultCh, "a result only goes to the scheduler that delivered it")
}

func TestPull_ResultHeldUntilAStreamConnects(t *testing.T) {
	t.Parallel()
	p := NewPull(t.Context())
	delivering := testStreamer(p, "sched-0")
	p.attach(delivering)

	release := runBlocked(p, delivering, 7, "wf::1::0", schedulerv1pb.WatchJobsRequestResultStatus_SUCCESS)
	close(delivering.done)
	p.detach(delivering)
	close(release)
	waitHeld(t, p)

	assert.Equal(t, []*schedulerv1pb.ActorReminderRef{wantRef("wf::1::0")}, p.refs("sched-0"),
		"a held result is listed so the scheduler keeps waiting for it")

	reconnected := testStreamer(p, "sched-0")
	p.attach(reconnected)
	require.True(t, p.flush(reconnected))
	got := next(t, reconnected).GetResult()
	assert.True(t, proto.Equal(wantRef("wf::1::0"), got.GetReminder()))
	assert.Equal(t, schedulerv1pb.WatchJobsRequestResultStatus_SUCCESS, got.GetStatus())
	assert.Empty(t, p.refs("sched-0"))
}

func TestPull_RemindersOutliveTheirStream(t *testing.T) {
	t.Parallel()
	p := NewPull(t.Context())
	s := testStreamer(p, "sched-0")
	p.attach(s)

	var ctxErr error
	var wg sync.WaitGroup
	wg.Add(1)
	release := make(chan struct{})
	p.run(s, 1, actorTarget("wf::1::0"), "run-activity", func(ctx context.Context) schedulerv1pb.WatchJobsRequestResultStatus {
		defer wg.Done()
		<-release
		ctxErr = ctx.Err()
		return schedulerv1pb.WatchJobsRequestResultStatus_SUCCESS
	})

	close(s.done)
	p.detach(s)
	close(release)
	wg.Wait()
	require.NoError(t, ctxErr, "a closed stream does not cancel the reminder")
}

func TestPull_OneReporterPerScheduler(t *testing.T) {
	t.Parallel()
	p := NewPull(t.Context())
	a, b := testStreamer(p, "sched-0"), testStreamer(p, "sched-0")
	c := testStreamer(p, "sched-1")
	p.attach(a)
	p.attach(b)
	p.attach(c)

	isA, changed := p.reporter(a)
	isB, _ := p.reporter(b)
	isC, _ := p.reporter(c)
	assert.True(t, isA)
	assert.False(t, isB)
	assert.True(t, isC)

	p.detach(a)
	select {
	case <-changed:
	default:
		require.Fail(t, "a reporter leaving must wake the others")
	}
	isB, _ = p.reporter(b)
	assert.True(t, isB, "the next stream to the scheduler takes over")
}

func TestStreamer_ReportPull(t *testing.T) {
	t.Parallel()

	var lock sync.Mutex
	free, changed := uint32(1), make(chan struct{})
	set := func(n uint32) {
		lock.Lock()
		defer lock.Unlock()
		free = n
		close(changed)
		changed = make(chan struct{})
	}

	p := NewPull(t.Context())
	reporter := testStreamer(p, "sched-0")
	reporter.wfengine = wfenginefake.New().WithActivityFreeSlots(func() (uint32, <-chan struct{}) {
		lock.Lock()
		defer lock.Unlock()
		return free, changed
	})
	follower := testStreamer(p, "sched-0")
	follower.wfengine = reporter.wfengine
	p.attach(reporter)
	p.attach(follower)

	ctx, cancel := context.WithCancel(t.Context())
	errCh := make(chan error, 2)
	go func() { errCh <- reporter.reportPull(ctx) }()
	go func() { errCh <- follower.reportPull(ctx) }()

	// A reporter resyncs when it starts reporting.
	assert.Equal(t, uint32(1), next(t, reporter).GetPullSlots().GetFree())

	set(0)
	got := next(t, reporter).GetPullSlots()
	assert.Equal(t, testActivityType, got.GetActorType())
	assert.Equal(t, uint32(0), got.GetFree())

	// The reporter leaves: the follower takes over and resyncs.
	p.detach(reporter)
	assert.Equal(t, uint32(0), next(t, follower).GetPullSlots().GetFree())
	assert.Empty(t, reporter.resultCh)

	cancel()
	for range 2 {
		<-errCh
	}
}

func TestConnector_InitialCarriesPullState(t *testing.T) {
	t.Parallel()
	p := NewPull(t.Context())
	s := testStreamer(p, "sched-0")
	p.attach(s)
	release := runBlocked(p, s, 1, "wf::1::0", schedulerv1pb.WatchJobsRequestResultStatus_SUCCESS)
	t.Cleanup(func() { close(release) })

	base := &schedulerv1pb.WatchJobsRequest{WatchJobRequestType: &schedulerv1pb.WatchJobsRequest_Initial{
		Initial: &schedulerv1pb.WatchJobsRequestInitial{AppId: "myapp"},
	}}
	c := &connector{
		req:           base,
		addr:          "sched-0",
		pull:          p,
		pullActorType: testActivityType,
		wfengine: wfenginefake.New().WithActivityFreeSlots(func() (uint32, <-chan struct{}) {
			return 3, nil
		}),
	}

	req := c.initial()
	assert.Equal(t, "myapp", req.GetInitial().GetAppId())
	require.Len(t, req.GetInitial().GetPullSlots(), 1)
	assert.Equal(t, uint32(3), req.GetInitial().GetPullSlots()[0].GetFree())
	assert.Equal(t, testActivityType, req.GetInitial().GetPullSlots()[0].GetActorType())
	require.Len(t, req.GetInitial().GetInflightReminders(), 1)
	assert.True(t, proto.Equal(wantRef("wf::1::0"), req.GetInitial().GetInflightReminders()[0]))
	assert.Empty(t, base.GetInitial().GetPullSlots(), "the shared request is not modified")

	c.pull = nil
	req = c.initial()
	assert.Same(t, base, req, "without pull dispatch the shared request is sent as is")
}

func TestPull_RetainDropsResultsOfDepartedSchedulers(t *testing.T) {
	t.Parallel()
	p := NewPull(t.Context())
	s := testStreamer(p, "sched-0")
	p.attach(s)
	release := runBlocked(p, s, 1, "wf::1::0", schedulerv1pb.WatchJobsRequestResultStatus_SUCCESS)
	close(s.done)
	p.detach(s)
	close(release)
	waitHeld(t, p)

	p.Retain([]string{"sched-0", "sched-1"})
	assert.Len(t, p.refs("sched-0"), 1)
	p.Retain([]string{"sched-1"})
	assert.Empty(t, p.refs("sched-0"))
}

func TestPull_FailedSendKeepsTheResult(t *testing.T) {
	t.Parallel()
	p := NewPull(t.Context())
	s := testStreamerSending(p, "sched-0", false)
	p.attach(s)

	close(runBlocked(p, s, 1, "wf::1::0", schedulerv1pb.WatchJobsRequestResultStatus_SUCCESS))
	waitHeld(t, p)
	assert.Equal(t, []*schedulerv1pb.ActorReminderRef{wantRef("wf::1::0")}, p.refs("sched-0"),
		"a result the stream failed to send is held and listed on reconnect")
}

func TestStreamer_ReportPullResendsAnUnchangedCount(t *testing.T) {
	t.Parallel()
	var lock sync.Mutex
	changed := make(chan struct{})
	p := NewPull(t.Context())
	s := testStreamer(p, "sched-0")
	s.wfengine = wfenginefake.New().WithActivityFreeSlots(func() (uint32, <-chan struct{}) {
		lock.Lock()
		defer lock.Unlock()
		return 1, changed
	})
	p.attach(s)

	ctx, cancel := context.WithCancel(t.Context())
	errCh := make(chan error, 1)
	go func() { errCh <- s.reportPull(ctx) }()
	assert.Equal(t, uint32(1), next(t, s).GetPullSlots().GetFree())

	// An activity reserves and frees a slot before the reporter looks again:
	// the count is unchanged, but the scheduler took a slot for the delivery
	// and must hear back.
	lock.Lock()
	close(changed)
	changed = make(chan struct{})
	lock.Unlock()
	assert.Equal(t, uint32(1), next(t, s).GetPullSlots().GetFree())

	cancel()
	<-errCh
}
