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

// base is embedded in all reconciler events to satisfy the Event interface.
type base struct{}

func (base) isEvent() {}

// Event is the typed event interface for the reconciler loop.
type Event interface{ isEvent() }

// Tick is sent periodically to trigger a scheduled reconcile.
type Tick struct{ base }

// Reconcile is sent to trigger a full reconcile of all resources, e.g. after
// a reconnect.
type Reconcile struct{ base }

// Resource carries a single resource change event.
type Resource struct {
	base
	// Event is a *loader.Event[T] at runtime.
	Event any
}
