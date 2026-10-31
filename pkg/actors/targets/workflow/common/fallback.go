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

package common

import (
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
)

// FallbackHeader is set on the response of a workflow actor method served by
// a fallback host (the WorkflowsStagedOperations preview): a daprd with no
// local executor, on which the instance does not advance until the actor
// moves to a daprd with one.
const FallbackHeader = "dapr-workflow-fallback"

// SetFallback marks res as served by a fallback host.
func SetFallback(res *internalsv1pb.InternalInvokeResponse) {
	if res.Headers == nil {
		res.Headers = make(map[string]*internalsv1pb.ListStringValue, 1)
	}
	res.Headers[FallbackHeader] = &internalsv1pb.ListStringValue{Values: []string{"true"}}
}

// IsFallback returns whether res was served by a fallback host.
func IsFallback(res *internalsv1pb.InternalInvokeResponse) bool {
	_, ok := res.GetHeaders()[FallbackHeader]
	return ok
}
