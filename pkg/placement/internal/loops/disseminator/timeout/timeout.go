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

package timeout

import (
	"time"

	"github.com/dapr/dapr/pkg/placement/internal/loops"
	"github.com/dapr/kit/events/loop"
	"github.com/dapr/kit/events/queue"
)

type Options struct {
	Loop    loop.Interface[loops.Event]
	Timeout time.Duration
}

type Timeout struct {
	queue   *queue.Processor[uint64, *dissemination]
	timeout time.Duration
}

func New(opts Options) *Timeout {
	return &Timeout{
		queue: queue.NewProcessor[uint64, *dissemination](queue.Options[uint64, *dissemination]{
			ExecuteFn: func(diss *dissemination) {
				opts.Loop.Enqueue(&loops.DisseminationTimeout{
					Version: diss.Key(),
				})
			},
		}),
		timeout: opts.Timeout,
	}
}

func (t *Timeout) Close() error {
	return t.queue.Close()
}

func (t *Timeout) Enqueue(version uint64) {
	t.queue.Enqueue(&dissemination{
		version: version,
		dueTime: time.Now().Add(t.timeout),
	})
}

func (t *Timeout) Dequeue(version uint64) {
	t.queue.Dequeue(version)
}
