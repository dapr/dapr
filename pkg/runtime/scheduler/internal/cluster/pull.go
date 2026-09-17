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
	"slices"
	"sync"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
)

// Pull is the sidecar's pull dispatch state, shared by all of its scheduler
// streams and kept across stream reconnects. It runs the pull reminders
// delivered to this sidecar under its own context, so they keep running when
// the stream that delivered them closes, and returns each result to a live
// stream of the scheduler that delivered it: by id on the delivering stream,
// or by reference on another. A reconnecting stream lists the reminders it
// holds so the scheduler waits for them instead of delivering them again.
// Pull also picks, per scheduler, the one stream that reports free slots.
type Pull struct {
	ctx context.Context

	lock    sync.Mutex
	jobs    map[pullJobKey]*pullJob
	streams map[string][]*streamer
	changed chan struct{}
}

type pullJobKey struct {
	addr, actorType, actorID, name string
}

type pullJob struct {
	key pullJobKey
	s   *streamer
	id  uint64
	// status is set when the reminder finished while no stream to its
	// scheduler was connected.
	status *schedulerv1pb.WatchJobsRequestResultStatus
}

// NewPull returns the pull state of a sidecar. Pull reminders run under ctx.
func NewPull(ctx context.Context) *Pull {
	return &Pull{
		ctx:     ctx,
		jobs:    make(map[pullJobKey]*pullJob),
		streams: make(map[string][]*streamer),
		changed: make(chan struct{}),
	}
}

// refs returns the pull reminders delivered by the scheduler at addr that
// this sidecar still holds.
func (p *Pull) refs(addr string) []*schedulerv1pb.ActorReminderRef {
	p.lock.Lock()
	defer p.lock.Unlock()
	var refs []*schedulerv1pb.ActorReminderRef
	for k := range p.jobs {
		if k.addr == addr {
			refs = append(refs, &schedulerv1pb.ActorReminderRef{ActorType: k.actorType, ActorId: k.actorID, Name: k.name})
		}
	}
	return refs
}

func (p *Pull) attach(s *streamer) {
	p.lock.Lock()
	defer p.lock.Unlock()
	p.streams[s.addr] = append(p.streams[s.addr], s)
	p.signalLocked()
}

func (p *Pull) detach(s *streamer) {
	p.lock.Lock()
	defer p.lock.Unlock()
	n := len(p.streams[s.addr])
	p.streams[s.addr] = slices.DeleteFunc(p.streams[s.addr], func(o *streamer) bool { return o == s })
	if len(p.streams[s.addr]) == 0 {
		delete(p.streams, s.addr)
	}
	if n != len(p.streams[s.addr]) {
		p.signalLocked()
	}
}

func (p *Pull) signalLocked() {
	close(p.changed)
	p.changed = make(chan struct{})
}

// reporter reports whether s is the stream that reports free slots to its
// scheduler, and returns a channel closed when that may change.
func (p *Pull) reporter(s *streamer) (bool, <-chan struct{}) {
	p.lock.Lock()
	defer p.lock.Unlock()
	streams := p.streams[s.addr]
	return len(streams) > 0 && streams[0] == s, p.changed
}

// run executes the pull reminder delivered on s as id under the Pull's
// context and returns its result to the scheduler that delivered it.
func (p *Pull) run(s *streamer, id uint64, actor *schedulerv1pb.TargetActorReminder, name string, fn func(context.Context) schedulerv1pb.WatchJobsRequestResultStatus) {
	job := &pullJob{
		key: pullJobKey{addr: s.addr, actorType: actor.GetType(), actorID: actor.GetId(), name: name},
		s:   s,
		id:  id,
	}
	p.lock.Lock()
	// A reminder the scheduler gave up waiting for and delivered again
	// replaces the earlier delivery here; the activity runtime joins the two.
	p.jobs[job.key] = job
	p.lock.Unlock()

	go func() {
		p.finish(job, fn(p.ctx))
	}()
}

// finish sends job's result to a live stream of its scheduler, or keeps it
// until one connects.
func (p *Pull) finish(job *pullJob, status schedulerv1pb.WatchJobsRequestResultStatus) {
	for {
		p.lock.Lock()
		current := p.jobs[job.key] == job
		streams := p.streams[job.key.addr]
		var target *streamer
		result := &schedulerv1pb.WatchJobsRequestResult{Status: status}
		switch {
		case slices.Contains(streams, job.s):
			target = job.s
			result.Id = job.id
		case current && len(streams) > 0:
			target = streams[0]
			result.Reminder = job.ref()
		case current:
			job.status = &status
		}
		p.lock.Unlock()

		if target == nil {
			return
		}
		if target.send(&schedulerv1pb.WatchJobsRequest{
			WatchJobRequestType: &schedulerv1pb.WatchJobsRequest_Result{Result: result},
		}) {
			p.remove(job)
			return
		}
		p.detach(target)
	}
}

// flush sends s the results of reminders of its scheduler that finished
// while no stream to it was connected.
func (p *Pull) flush(s *streamer) bool {
	p.lock.Lock()
	var pending []*pullJob
	for _, job := range p.jobs {
		if job.key.addr == s.addr && job.status != nil {
			pending = append(pending, job)
		}
	}
	p.lock.Unlock()

	for _, job := range pending {
		if !s.send(&schedulerv1pb.WatchJobsRequest{
			WatchJobRequestType: &schedulerv1pb.WatchJobsRequest_Result{Result: &schedulerv1pb.WatchJobsRequestResult{
				Status:   *job.status,
				Reminder: job.ref(),
			}},
		}) {
			return false
		}
		p.remove(job)
	}
	return true
}

// Retain drops the held results of schedulers no longer in addrs: no stream
// to them will connect to take the results.
func (p *Pull) Retain(addrs []string) {
	p.lock.Lock()
	defer p.lock.Unlock()
	for key, job := range p.jobs {
		if job.status != nil && !slices.Contains(addrs, key.addr) {
			delete(p.jobs, key)
		}
	}
}

func (p *Pull) remove(job *pullJob) {
	p.lock.Lock()
	defer p.lock.Unlock()
	if p.jobs[job.key] == job {
		delete(p.jobs, job.key)
	}
}

func (j *pullJob) ref() *schedulerv1pb.ActorReminderRef {
	return &schedulerv1pb.ActorReminderRef{ActorType: j.key.actorType, ActorId: j.key.actorID, Name: j.key.name}
}
