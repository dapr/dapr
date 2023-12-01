/*
Copyright 2023 The Dapr Authors
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

package sqlite

// options contains the options for using a SQLite database in integration tests.
type options struct {
	name            string
	actorStateStore bool
	metadata        map[string]string
}

// WithName sets the name for the state store.
// Default is "mystore".
func WithName(name string) Option {
	return func(o *options) {
		o.name = name
	}
}

// WithActorStateStore configures whether the state store is enabled as actor state store.
func WithActorStateStore(enabled bool) Option {
	return func(o *options) {
		o.actorStateStore = enabled
	}
}

// WithMetadata adds a metadata option to the component.
// This can be invoked multiple times.
func WithMetadata(key, value string) Option {
	return func(o *options) {
		o.metadata[key] = value
	}
}
