/*
Copyright 2025 The Dapr Authors
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

package loops

import (
	"context"

	"github.com/diagridio/go-etcd-cron/api"

	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
)

// Event is a generic interface for all events in the scheduler.
type Event any

// TriggerRequest is the event for triggering a job in the scheduler.
type TriggerRequest struct {
	Job      *internalsv1pb.JobEvent
	ResultFn func(api.TriggerResponseResult)
}

// StreamShutdown is the event for shutting down the scheduler stream.
type StreamShutdown struct{}

// ConnAdd is the event for adding a new connection to the scheduler.
type ConnAdd struct {
	Channel schedulerv1pb.Scheduler_WatchJobsServer
	Request *schedulerv1pb.WatchJobsRequestInitial
	Cancel  context.CancelCauseFunc
}

// ConnCloseStream is the event for closing a connection to the scheduler from
// the stream.
type ConnCloseStream struct {
	StreamIDx uint64
	Namespace string
}

// Shutdown is the event for shutting down the scheduler loops.
type Shutdown struct{}
