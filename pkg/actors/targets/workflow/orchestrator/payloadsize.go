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

package orchestrator

import (
	"errors"
	"fmt"

	"google.golang.org/protobuf/proto"

	wfenginestate "github.com/dapr/dapr/pkg/runtime/wfengine/state"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
)

// errPayloadSizeExceeded is returned by activityPayloadOversize when the
// synthesized ActivityRequest would exceed the gRPC stream's send threshold.
var errPayloadSizeExceeded = errors.New("activity payload exceeds max gRPC body size")

// payloadStallNumerator and payloadStallDenominator define the fraction of the
// gRPC server's max message size at which the orchestrator stalls a workflow
// rather than letting the GetWorkItems stream send fail with
// ResourceExhausted. The headroom covers the WorkflowStarted event that the
// engine appends in applyWorkItem and gRPC framing overhead.
const (
	payloadStallNumerator   = 95
	payloadStallDenominator = 100
)

// workflowPayloadOversize reports whether the WorkflowRequest the executor
// will build for this run would exceed the gRPC stream's send threshold.
// A non-positive maxRequestBodySize signals "no limit" (matching the dapr
// HTTP server's convention) and disables the precheck.
func (o *orchestrator) workflowPayloadOversize(state *wfenginestate.State) (protos.StalledReason, string, bool) {
	if o.maxRequestBodySize <= 0 {
		return 0, "", false
	}
	threshold := (o.maxRequestBodySize * payloadStallNumerator) / payloadStallDenominator
	var size int
	for _, e := range state.History {
		size += proto.Size(e)
	}
	for _, e := range state.Inbox {
		size += proto.Size(e)
	}
	if size <= threshold {
		return 0, "", false
	}

	return protos.StalledReason_PAYLOAD_SIZE_EXCEEDED,
		fmt.Sprintf("Workflow payload size %d bytes exceeds %d%% of max gRPC body size %d bytes; increase daprd --max-body-size and restart to resume",
			size, payloadStallNumerator, o.maxRequestBodySize),
		true
}

// activityPayloadOversize reports whether the ActivityRequest the executor
// will build for this dispatch would exceed the gRPC stream's send threshold.
// Returns nil when the payload fits, or a wrapped errPayloadSizeExceeded that
// runWorkflow uses to stall the parent workflow. A non-positive
// maxRequestBodySize signals "no limit" and disables the precheck.
func (o *orchestrator) activityPayloadOversize(payload proto.Message, e *backend.HistoryEvent) error {
	if o.maxRequestBodySize <= 0 {
		return nil
	}
	threshold := (o.maxRequestBodySize * payloadStallNumerator) / payloadStallDenominator
	size := proto.Size(payload)
	if size <= threshold {
		return nil
	}

	ts := e.GetTaskScheduled()
	return fmt.Errorf("activity '%s::%d' payload %d bytes exceeds %d%% of max gRPC body size %d bytes; increase daprd --max-body-size and restart to resume: %w",
		ts.GetName(), e.GetEventId(), size, payloadStallNumerator, o.maxRequestBodySize, errPayloadSizeExceeded)
}
