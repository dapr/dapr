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

package reconciler

import (
	"github.com/dapr/dapr/pkg/runtime/hotreload/differ"
	"github.com/dapr/dapr/pkg/runtime/hotreload/loader"
)

type eventbase struct{}

func (*eventbase) isEvent() {}

// Event is a marker interface for reconciler loop events.
type Event interface{ isEvent() }

// tick signals a periodic reconciliation.
type tick struct {
	*eventbase
}

// reconcile signals a full reconciliation (e.g., after reconnect).
type reconcile struct {
	*eventbase
}

// resourceEvent represents a single resource event from the loader.
type resourceEvent[T differ.Resource] struct {
	*eventbase
	Event *loader.Event[T]
}

// shutdown signals graceful shutdown.
type shutdown struct {
	*eventbase
	Error error
}
