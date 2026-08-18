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

package loops

import (
	httpendpointsapi "github.com/dapr/dapr/pkg/apis/httpEndpoint/v1alpha1"
)

// AddHTTPEndpoint adds an HTTP endpoint. Routed by the root loop to the
// per-name HTTPEndpoint loop so events for one endpoint name serialise in
// submission order. The event satisfies both EventRoot (so callers enqueue at
// the root) and EventHTTPEndpoint (so the per-name loop can pull it).
type AddHTTPEndpoint struct {
	*rootbase
	*httpbase
	Endpoint httpendpointsapi.HTTPEndpoint
	Result   chan<- error
}

// HTTPEndpointAdded is the per-name HTTPEndpoint loop's notification back to
// the root loop that an Add has finished (secret resolution + compstore
// write). Used to decrement the root's in-flight counter so Barrier
// completion can fire once every queued Add has drained.
type HTTPEndpointAdded struct {
	*rootbase
}
