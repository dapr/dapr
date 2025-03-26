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

package connection

import (
	"context"
	"sync"

	"github.com/diagridio/go-etcd-cron/api"
	"google.golang.org/protobuf/types/known/anypb"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
)

// JobEvent is a triggered job event.
type JobEvent struct {
	Name     string
	Data     *anypb.Any
	Metadata *schedulerv1pb.JobMetadata
}

// Connection represents a single connection bidirectional stream between the
// scheduler and the client (daprd). conn manages sending triggered jobs to the
// client, and receiving job process results from the client. Jobs are sent
// serially via a channel as gRPC does not support concurrent sends. conn
// tracks the inflight jobs and acks them when the client sends back the
// result, releasing the job triggering.
type Connection struct {
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

func New() *Connection {
	return &Connection{
		inflight: make(map[uint64]chan schedulerv1pb.WatchJobsRequestResultStatus),
		jobCh:    make(chan *schedulerv1pb.WatchJobsResponse, 10),
		closeCh:  make(chan struct{}),
	}
}

// Handle sends a job to the client and waits for the client to send back the
// result. The job is acked when the client sends back the result with a UUID
// corresponding to the job.
func (c *Connection) Handle(ctx context.Context, jobEvt *JobEvent) api.TriggerResponseResult {
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
	case <-ctx.Done():
		return api.TriggerResponseResult_FAILED
	case <-c.closeCh:
		return api.TriggerResponseResult_FAILED
	}

	select {
	case result := <-ackCh:
		if result == schedulerv1pb.WatchJobsRequestResultStatus_SUCCESS {
			return api.TriggerResponseResult_SUCCESS
		}
		return api.TriggerResponseResult_FAILED
	case <-ctx.Done():
		return api.TriggerResponseResult_FAILED
	case <-c.closeCh:
		return api.TriggerResponseResult_FAILED
	}
}

// Ack acks the job with the given UUID. This is called when the client
// sends back the result of the job to be acked.
func (c *Connection) Ack(ctx context.Context, result *schedulerv1pb.WatchJobsRequestResult) {
	c.lock.Lock()
	defer c.lock.Unlock()

	if ch, ok := c.inflight[result.GetId()]; ok {
		select {
		case ch <- result.GetStatus():
		case <-ctx.Done():
		}
	}

	delete(c.inflight, result.GetId())
}

func (c *Connection) Close() {
	close(c.closeCh)
}

func (c *Connection) Next(ctx context.Context) (*schedulerv1pb.WatchJobsResponse, error) {
	select {
	case job := <-c.jobCh:
		return job, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
