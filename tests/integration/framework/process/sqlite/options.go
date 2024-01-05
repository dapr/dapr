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
	name              string
	isActorStateStore bool
	metadata          map[string]string
	migrations        []string
	execs             []string
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
		o.isActorStateStore = enabled
	}
}

// WithMetadata adds a metadata option to the component.
// This can be invoked multiple times.
func WithMetadata(key, value string) Option {
	return func(o *options) {
		if o.metadata == nil {
			o.metadata = make(map[string]string)
		}
		o.metadata[key] = value
	}
}

// WithCreateStateTables configures whether the state store should create the state tables.
func WithCreateStateTables() Option {
	return func(o *options) {
		o.migrations = append(o.migrations, `
CREATE TABLE metadata (
  key text NOT NULL PRIMARY KEY,
  value text NOT NULL
);
INSERT INTO metadata VALUES('migrations','1');
CREATE TABLE state (
  key TEXT NOT NULL PRIMARY KEY,
  value TEXT NOT NULL,
  is_binary BOOLEAN NOT NULL,
  etag TEXT NOT NULL,
  expiration_time TIMESTAMP DEFAULT NULL,
  update_time TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);`)
	}
}

// WithExecs defines the execs to run against the database on Run.
func WithExecs(execs ...string) Option {
	return func(o *options) {
		o.execs = execs
	}
}
