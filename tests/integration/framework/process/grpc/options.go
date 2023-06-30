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

package grpc

import (
	"google.golang.org/grpc"
)

// options contains the options for running a GRPC server in integration tests.
type options struct {
	registerFns []func(*grpc.Server)
}

func WithRegister(f func(*grpc.Server)) Option {
	return func(o *options) {
		o.registerFns = append(o.registerFns, f)
	}
}
