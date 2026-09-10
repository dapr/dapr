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

package placement

import (
	"time"
)

// Option is a function that configures the process.
type Option func(*options)

type options struct {
	disseminateTimeout time.Duration
}

// WithDisseminateTimeout enables the Config RPC, advertising the given
// dissemination timeout. When not set, Config returns Unimplemented,
// simulating a placement server that predates the RPC.
func WithDisseminateTimeout(timeout time.Duration) Option {
	return func(o *options) {
		o.disseminateTimeout = timeout
	}
}
