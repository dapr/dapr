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

// Package timeout schedules dissemination round timeouts, keyed by round seq.
package timeout

import (
	"time"

	"github.com/dapr/dapr/pkg/scheduler/server/internal/placement/loops"
	"github.com/dapr/kit/events/loop"
	"github.com/dapr/kit/events/queue"
)

type Options struct {
	Loop    loop.Interface[loops.EventConnections]
	Timeout time.Duration
}

type Timeout struct {
	queue   *queue.Processor[uint64, *round]
	timeout time.Duration
}

type round struct {
	seq     uint64
	dueTime time.Time
}

func (r *round) Key() uint64 {
	return r.seq
}

func (r *round) ScheduledTime() time.Time {
	return r.dueTime
}

func New(opts Options) *Timeout {
	return &Timeout{
		queue: queue.NewProcessor[uint64, *round](queue.Options[uint64, *round]{
			ExecuteFn: func(r *round) {
				opts.Loop.Enqueue(&loops.RoundTimeout{
					Seq: r.Key(),
				})
			},
		}),
		timeout: opts.Timeout,
	}
}

func (t *Timeout) Close() error {
	return t.queue.Close()
}

func (t *Timeout) Enqueue(seq uint64) {
	t.queue.Enqueue(&round{
		seq:     seq,
		dueTime: time.Now().Add(t.timeout),
	})
}

func (t *Timeout) Dequeue(seq uint64) {
	t.queue.Dequeue(seq)
}
