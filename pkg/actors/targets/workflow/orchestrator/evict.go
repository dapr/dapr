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
	"context"
	"net/http"

	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
)

// evict deactivates this actor for a force purge served by another daprd,
// which cannot reach the actor's cache any other way. Deactivation takes the
// turn lock, so it lands after any in-flight turn commits, and it releases a
// stalled actor the same way the purging daprd's own halt does. Only this
// app's daprds may evict: the access policy refuses internal methods to every
// other caller.
func (o *orchestrator) evict(ctx context.Context, req *internalsv1pb.InternalInvokeRequest) (*internalsv1pb.InternalInvokeResponse, error) {
	if err := o.checkAccessPolicy(ctx, todo.EvictWorkflowMethod, nil, nil, nil, req.GetMetadata()); err != nil {
		return nil, err
	}

	log.Debugf("Workflow actor '%s': evicting for a force purge on another daprd", o.actorID)

	// An actor another call already retired is evicted: deactivating it again
	// would wait on a closed lock.
	if !o.isRetired() {
		if err := o.Deactivate(ctx); err != nil && !o.isRetired() {
			return nil, err
		}
	}

	return &internalsv1pb.InternalInvokeResponse{
		Status: &internalsv1pb.Status{Code: http.StatusOK},
	}, nil
}

func (o *orchestrator) isRetired() bool {
	o.retireLock.Lock()
	defer o.retireLock.Unlock()
	return o.retired
}
