/*
Copyright 2024 The Dapr Authors
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

package internal

import (
	"context"
	"sync"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
)

type conn struct {
	wg       sync.WaitGroup
	closeCh  chan struct{}
	streamer *streamer
	ch       chan *schedulerv1pb.WatchJobsResponse
}

type streamer struct {
	lock sync.RWMutex
	subs map[uint32]chan struct{}
}

func (s *streamer) add(id uint32) chan struct{} {
	s.lock.Lock()
	defer s.lock.Unlock()
	ch := make(chan struct{})
	s.subs[id] = ch
	return ch
}

func (s *streamer) handleResponse(id uint32) {
	s.lock.Lock()
	defer s.lock.Unlock()
	if ch, ok := s.subs[id]; ok {
		ch <- struct{}{}
	}
	delete(s.subs, id)
}

func (p *Pool) sendWaitForResponse(ctx context.Context, conn *conn, job *schedulerv1pb.WatchJobsResponse) {
	// TODO: @joshvanl
	// ack := conn.streamer.add(job.Uuid)
	_ = conn.streamer.add(job.GetUuid())

	select {
	case conn.ch <- job:
	case <-p.closeCh:
	case <-conn.closeCh:
	case <-ctx.Done():
	}

	// TODO: @joshvanl
	// select {
	// case <-ack:
	// case <-p.closeCh:
	// case <-conn.closeCh:
	// case <-ctx.Done():
	// }
}
