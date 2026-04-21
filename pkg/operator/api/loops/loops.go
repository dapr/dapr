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
	operatorv1pb "github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/dapr/dapr/pkg/runtime/meta"
)

// EventClient is the marker interface for events handled by the client loop.
type EventClient interface{ isEventClient() }

type clientbase struct{}

func (*clientbase) isEventClient() {}

// EventStream is the marker interface for events handled by the stream loop.
type EventStream interface{ isEventStream() }

type streambase struct{}

func (*streambase) isEventStream() {}

// ResourceUpdate is an event for a component update from the informer.
type ResourceUpdate[T meta.Resource] struct {
	*clientbase
	Resource  T
	EventType operatorv1pb.ResourceEventType
}

// StreamSend is an event to send a message over the gRPC stream.
type StreamSend struct {
	*streambase
	Resource  []byte
	EventType operatorv1pb.ResourceEventType
}

// Shutdown signals graceful shutdown.
type Shutdown struct {
	*clientbase
	*streambase
	Error error
}
