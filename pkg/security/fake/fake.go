//go:build unit
// +build unit

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

package fake

import (
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type Fake struct {
	grpcServerOptionFn             func() grpc.ServerOption
	grpcServerOptionNoClientAuthFn func() grpc.ServerOption
}

func New() *Fake {
	return &Fake{
		grpcServerOptionFn: func() grpc.ServerOption {
			return grpc.Creds(insecure.NewCredentials())
		},
		grpcServerOptionNoClientAuthFn: func() grpc.ServerOption {
			return grpc.Creds(insecure.NewCredentials())
		},
	}
}

func (f *Fake) WithGRPCServerOptionNoClientAuthFn(fn func() grpc.ServerOption) *Fake {
	f.grpcServerOptionNoClientAuthFn = fn
	return f
}

func (f *Fake) WithGRPCServerOptionFn(fn func() grpc.ServerOption) *Fake {
	f.grpcServerOptionFn = fn
	return f
}

func (f *Fake) GRPCServerOptionNoClientAuth() grpc.ServerOption {
	return f.grpcServerOptionNoClientAuthFn()
}

func (f *Fake) GRPCServerOption() grpc.ServerOption {
	return f.grpcServerOptionFn()
}
