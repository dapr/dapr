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

package executor

import (
	"context"
	"strconv"

	"google.golang.org/grpc/codes"

	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
)

const (
	// MetadataCompletionToken carries the completion token of a forwarded
	// completion, so the daprd that delivers it can match it to the work
	// item its result ends in.
	MetadataCompletionToken = "completiontoken"

	// MetadataAwaitSaved asks the daprd that delivers a forwarded activity
	// completion to wait for its result to be saved, because the worker
	// asked to wait. A workflow completion is always waited for.
	MetadataAwaitSaved = "awaitsaved"

	// MetadataSaved is the response header of a Complete call that waited
	// for the delivered result to be saved: "true" when it was saved, and
	// "false" when it was not. A call that delivered nothing on this daprd,
	// or whose result no work item took, does not set it.
	MetadataSaved = "saved"
)

// awaitsSave reports whether a Complete call waits for the result it
// delivers to be saved.
func awaitsSave(req *internalsv1pb.InternalInvokeRequest, taskType string) bool {
	if taskType == TaskTypeWorkflow {
		return true
	}
	v, ok := req.GetMetadata()[MetadataAwaitSaved]
	return ok && len(v.GetValues()) > 0 && v.GetValues()[0] == "true"
}

func completionTokenOf(req *internalsv1pb.InternalInvokeRequest) string {
	if v, ok := req.GetMetadata()[MetadataCompletionToken]; ok && len(v.GetValues()) > 0 {
		return v.GetValues()[0]
	}
	return ""
}

// deliverDurably runs run, which hands a completion to its waiter on this
// daprd, and then waits for the result to be saved when a work item took
// it. The answer carries MetadataSaved, so the daprd that forwarded the
// completion answers the worker as if it had delivered it itself. A
// workflow completion always waits, and an activity completion only when
// the worker asked to wait (see awaitsSave).
func (e *executor) deliverDurably(ctx context.Context, req *internalsv1pb.InternalInvokeRequest, run func()) (*internalsv1pb.InternalInvokeResponse, error) {
	if e.durables == nil || !awaitsSave(req, taskTypeOf(req, e.actorID)) {
		run()
		return nil, nil
	}

	receipt := e.durables.Expect(e.actorID, completionTokenOf(req))
	run()
	durable := receipt.Close()
	if durable == nil {
		return nil, nil
	}

	// A save that settled as ctx ended is still reported.
	select {
	case <-durable.Done():
	default:
		select {
		case <-durable.Done():
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	return &internalsv1pb.InternalInvokeResponse{
		Status: &internalsv1pb.Status{Code: int32(codes.OK)},
		Headers: map[string]*internalsv1pb.ListStringValue{
			MetadataSaved: {Values: []string{strconv.FormatBool(durable.Err() == nil)}},
		},
	}, nil
}
