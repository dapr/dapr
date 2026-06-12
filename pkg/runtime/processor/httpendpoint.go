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

package processor

import (
	"context"

	httpendpointsapi "github.com/dapr/dapr/pkg/apis/httpEndpoint/v1alpha1"
	"github.com/dapr/dapr/pkg/runtime/processor/loops"
)

// AddPendingEndpoint enqueues an HTTP endpoint and returns a result chan.
func (p *Processor) AddPendingEndpoint(ctx context.Context, endpoint httpendpointsapi.HTTPEndpoint) <-chan error {
	if p.closed.Load() {
		return nil
	}
	res := make(chan error, 1)
	p.rootLoop.Loop().Enqueue(&loops.AddHTTPEndpoint{Endpoint: endpoint, Result: res})
	return res
}
