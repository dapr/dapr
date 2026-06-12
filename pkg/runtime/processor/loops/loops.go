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
	"github.com/dapr/kit/events/loop"
)

// Event level markers. Events embed pointers to these base types to satisfy
// the corresponding interface at multiple levels with a single allocation.
type rootbase struct{}

func (*rootbase) isEventRoot() {}

type catbase struct{}

func (*catbase) isEventCategory() {}

type instbase struct{}

func (*instbase) isEventInstance() {}

type mcpbase struct{}

func (*mcpbase) isEventMCPServer() {}

type httpbase struct{}

func (*httpbase) isEventHTTPEndpoint() {}

// EventRoot is the universe of events the root processor loop handles.
type EventRoot interface{ isEventRoot() }

// EventCategory is the universe of events a per-category loop handles.
type EventCategory interface{ isEventCategory() }

// EventInstance is the universe of events a per-instance loop handles.
type EventInstance interface{ isEventInstance() }

// EventMCPServer is the universe of events a per-name MCPServer loop handles.
// MCPServers have no component category but still need per-name FIFO ordering
// so that an Add followed by a Delete (or two Adds) for the same name are
// processed in submission order.
type EventMCPServer interface{ isEventMCPServer() }

// EventHTTPEndpoint is the universe of events a per-name HTTPEndpoint loop
// handles. Same reasoning as EventMCPServer: HTTPEndpoint secret resolution
// can do real network I/O against the configured secret store, so events for
// one endpoint name run on their own lane.
type EventHTTPEndpoint interface{ isEventHTTPEndpoint() }

// InstanceInitDone is sent from the root's finalizer goroutine back into the
// root loop after a category/instance init completes. The root uses this to
// flush dependents waiting on a secret store coming online.
type InstanceInitDone struct {
	*rootbase
	Category string
	Name     string
	Err      error
	UserChan chan<- error
}

// Barrier resolves once it has been processed by the root loop. Used to flush
// outstanding events without blocking on a per-resource result.
type Barrier struct {
	*rootbase
	Done chan struct{}
}

// Shutdown is the sentinel passed to loop.Close at every level.
type Shutdown struct {
	*rootbase
	*catbase
	*instbase
	*mcpbase
	*httpbase
}

// Factories pool segments and loops to reduce GC pressure under load.
// Segment sizes match the expected event rate at each level.
var (
	RootFactory         = loop.New[EventRoot](64)
	CategoryFactory     = loop.New[EventCategory](32)
	InstanceFactory     = loop.New[EventInstance](8)
	MCPServerFactory    = loop.New[EventMCPServer](8)
	HTTPEndpointFactory = loop.New[EventHTTPEndpoint](8)
)
