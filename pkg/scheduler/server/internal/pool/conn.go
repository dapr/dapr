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

package pool

import (
	"context"
	"errors"
	"io"
	"sync"

	"github.com/diagridio/go-etcd-cron/api"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

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
	inflight map[uint64]chan schedulerv1pb.WatchJobsRequestResultStatus

	lock    sync.RWMutex
	closeCh chan struct{}
}

// newConn creates a new connection and starts the goroutines to handle sending
// jobs to the client and receiving job process results from the client.
func (p *Pool) newConn(ctx context.Context,
	req *schedulerv1pb.WatchJobsRequestInitial,
	stream schedulerv1pb.Scheduler_WatchJobsServer,
	id uint64,
	cancel context.CancelFunc,
) *conn {
	conn := &conn{
		pool:     p,
		closeCh:  make(chan struct{}),
		inflight: make(map[uint64]chan schedulerv1pb.WatchJobsRequestResultStatus),
		jobCh:    make(chan *schedulerv1pb.WatchJobsResponse, 10),
	}

	p.wg.Add(2)

	go func() {
		defer func() {
			cancel()
			log.Debugf("Closed send connection to %s/%s", req.GetNamespace(), req.GetAppId())
			p.wg.Done()
		}()

		for {
			select {
			case job := <-conn.jobCh:
				if err := stream.Send(job); err != nil {
					log.Warnf("Error sending job to connection %s/%s: %s", req.GetNamespace(), req.GetAppId(), err)
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	go func() {
		defer func() {
			cancel()
			log.Debugf("Closed receive connection to %s/%s", req.GetNamespace(), req.GetAppId())
			p.wg.Done()
		}()

		for {
			resp, err := stream.Recv()
			if err == nil {
				conn.handleJobProcessed(resp.GetResult())
				continue
			}

			isEOF := errors.Is(err, io.EOF)
			s, ok := status.FromError(err)
			if stream.Context().Err() != nil || isEOF || (ok && s.Code() != codes.Canceled) {
				return
			}

			log.Warnf("Error receiving from connection %s/%s: %s", req.GetNamespace(), req.GetAppId(), err)
			return
		}
	}()

	return conn
}

// sendWaitForResponse sends a job to the client and waits for the client to
// send back the result. The job is acked when the client sends back the result
// with a UUID corresponding to the job.
func (c *conn) sendWaitForResponse(ctx context.Context, jobEvt *JobEvent) api.TriggerResponseResult {
	c.lock.Lock()
	c.idx++
	ackCh := make(chan schedulerv1pb.WatchJobsRequestResultStatus, 1)
	c.inflight[c.idx] = ackCh
	job := &schedulerv1pb.WatchJobsResponse{
		Name:     jobEvt.Name,
		Id:       c.idx,
		Data:     jobEvt.Data,
		Metadata: jobEvt.Metadata,
	}
	c.lock.Unlock()

	defer func() {
		c.lock.Lock()
		delete(c.inflight, job.GetId())
		c.lock.Unlock()
	}()

	select {
	case c.jobCh <- job:
	case <-c.pool.closeCh:
		return api.TriggerResponseResult_FAILED
	case <-c.closeCh:
		return api.TriggerResponseResult_FAILED
	case <-ctx.Done():
		return api.TriggerResponseResult_FAILED
	}

	select {
	case result := <-ackCh:
		if result == schedulerv1pb.WatchJobsRequestResultStatus_SUCCESS {
			return api.TriggerResponseResult_SUCCESS
		}
	case <-c.pool.closeCh:
	case <-c.closeCh:
	case <-ctx.Done():
	}
	return api.TriggerResponseResult_FAILED
}

// handleJobProcessed acks the job with the given UUID. This is called when the
// client sends back the result of the job to be acked.
func (c *conn) handleJobProcessed(result *schedulerv1pb.WatchJobsRequestResult) {
	c.lock.Lock()
	defer c.lock.Unlock()

	if ch, ok := c.inflight[result.GetId()]; ok {
		select {
		case ch <- result.GetStatus():
		case <-c.closeCh:
		case <-c.pool.closeCh:
		}
	}

	delete(c.inflight, result.GetId())
}
