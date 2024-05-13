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
	"errors"
	"io"
	"sync"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
)

// conn represents a single connection bidirectional stream between the
// scheduler and the client (daprd). conn manages sending triggered jobs to the
// client, and receiving job process results from the client. Jobs are sent
// serially via a channel as gRPC does not support concurrent sends. conn
// tracks the inflight jobs and acks them when the client sends back the
// result, releasing the job triggering.
type conn struct {
	pool  *Pool
	jobCh chan *schedulerv1pb.WatchJobsResponse

	// idx is the uuid of a triggered job. We can use a simple counter as there
	// are no privacy/time attack concerns as this counter is scoped to a single
	// client.
	idx uint64

	// inflight tracks the jobs that have been sent to the client but have not
	// been acked yet.
	inflight map[uint64]chan struct{}

	lock    sync.RWMutex
	closeCh chan struct{}
}

// newConn creates a new connection and starts the goroutines to handle sending
// jobs to the client and receiving job process results from the client.
func (p *Pool) newConn(req *schedulerv1pb.WatchJobsRequestInitial, stream schedulerv1pb.Scheduler_WatchJobsServer, uuid uint64) *conn {
	conn := &conn{
		pool:     p,
		closeCh:  make(chan struct{}),
		inflight: make(map[uint64]chan struct{}),
		jobCh:    make(chan *schedulerv1pb.WatchJobsResponse, 10),
	}

	p.wg.Add(2)

	go func() {
		defer p.wg.Done()
		defer p.remove(req, uuid)
		defer close(conn.closeCh)
		for {
			select {
			case job := <-conn.jobCh:
				if err := stream.Send(job); err != nil {
					log.Warnf("Error sending job to connection %s/%s: %s", req.GetNamespace(), req.GetAppId(), err)
				}
			case <-p.closeCh:
				return
			case <-stream.Context().Done():
				return
			}
		}
	}()

	go func() {
		defer p.wg.Done()

		for {
			resp, err := stream.Recv()
			if errors.Is(err, context.Canceled) || errors.Is(err, io.EOF) {
				return
			}
			if err != nil {
				log.Warnf("Error receiving from connection: %v", err)
				return
			}

			conn.handleJobProcessed(resp.GetResult().GetUuid())
		}
	}()

	return conn
}

// sendWaitForResponse sends a job to the client and waits for the client to
// send back the result. The job is acked when the client sends back the result
// with a UUID corresponding to the job.
func (c *conn) sendWaitForResponse(ctx context.Context, jobEvt *JobEvent) {
	c.lock.Lock()
	c.idx++
	ackCh := make(chan struct{}, 1)
	c.inflight[c.idx] = ackCh
	job := &schedulerv1pb.WatchJobsResponse{
		Name:     jobEvt.Name,
		Uuid:     c.idx,
		Data:     jobEvt.Data,
		Metadata: jobEvt.Metadata,
	}
	c.lock.Unlock()

	defer func() {
		c.lock.Lock()
		delete(c.inflight, job.GetUuid())
		c.lock.Unlock()
	}()

	select {
	case c.jobCh <- job:
	case <-c.pool.closeCh:
	case <-c.closeCh:
	case <-ctx.Done():
	}

	select {
	case <-ackCh:
	case <-c.pool.closeCh:
	case <-c.closeCh:
	case <-ctx.Done():
	}
}

// handleJobProcessed acks the job with the given UUID. This is called when the
// client sends back the result of the job to be acked.
func (c *conn) handleJobProcessed(id uint64) {
	c.lock.Lock()
	defer c.lock.Unlock()

	if ch, ok := c.inflight[id]; ok {
		select {
		case ch <- struct{}{}:
		case <-c.closeCh:
		case <-c.pool.closeCh:
		}
	}

	delete(c.inflight, id)
}
