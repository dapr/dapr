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

package statestore

import (
	"github.com/dapr/components-contrib/state"
	"github.com/dapr/dapr/tests/integration/framework/socket"
)

// options contains the options for running a pluggable state store in integration tests.
type options struct {
	socket     *socket.Socket
	statestore state.Store

	getErr        error
	bulkGetErr    error
	bulkDeleteErr error
	queryErr      error
	bulkSetErr    error
	deleteErr     error
	setErr        error
}

func WithSocket(socket *socket.Socket) Option {
	return func(o *options) {
		o.socket = socket
	}
}

func WithStateStore(store state.Store) Option {
	return func(o *options) {
		o.statestore = store
	}
}

func WithGetError(err error) Option {
	return func(o *options) {
		o.getErr = err
	}
}

func WithBulkGetError(err error) Option {
	return func(o *options) {
		o.bulkGetErr = err
	}
}

func WithBulkDeleteError(err error) Option {
	return func(o *options) {
		o.bulkDeleteErr = err
	}
}

func WithQueryError(err error) Option {
	return func(o *options) {
		o.queryErr = err
	}
}

func WithBulkSetError(err error) Option {
	return func(o *options) {
		o.bulkSetErr = err
	}
}

func WithDeleteError(err error) Option {
	return func(o *options) {
		o.deleteErr = err
	}
}

func WithSetError(err error) Option {
	return func(o *options) {
		o.setErr = err
	}
}
