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

package app

import (
	"context"
	"testing"

	"google.golang.org/grpc"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	procgrpc "github.com/dapr/dapr/tests/integration/framework/process/grpc"
	testpb "github.com/dapr/dapr/tests/integration/framework/process/grpc/app/proto"
)

// Option is a function that configures the process.
type Option func(*options)

// App is a wrapper around a grpc.Server that implements a Dapr App.
type App struct {
	grpc *procgrpc.GRPC
}

func New(t *testing.T, fopts ...Option) *App {
	t.Helper()

	var opts options
	for _, fopt := range fopts {
		fopt(&opts)
	}

	return &App{
		grpc: procgrpc.New(t, append(opts.grpcopts, procgrpc.WithRegister(func(s *grpc.Server) {
			srv := &server{
				onInvokeFn:         opts.onInvokeFn,
				onJobEventFn:       opts.onJobEventFn,
				onTopicEventFn:     opts.onTopicEventFn,
				onBulkTopicEventFn: opts.onBulkTopicEventFn,
				listTopicSubFn:     opts.listTopicSubFn,
				listInputBindFn:    opts.listInputBindFn,
				onBindingEventFn:   opts.onBindingEventFn,
				healthCheckFn:      opts.healthCheckFn,
				pingFn:             opts.pingFn,
			}
			rtv1.RegisterAppCallbackServer(s, srv)
			rtv1.RegisterAppCallbackAlphaServer(s, srv)
			rtv1.RegisterAppCallbackHealthCheckServer(s, srv)
			testpb.RegisterTestServiceServer(s, srv)
			if opts.withRegister != nil {
				opts.withRegister(s)
			}
		}))...),
	}
}

func (a *App) Run(t *testing.T, ctx context.Context) {
	a.grpc.Run(t, ctx)
}

func (a *App) Cleanup(t *testing.T) {
	a.grpc.Cleanup(t)
}

func (a *App) Port(t *testing.T) int {
	return a.grpc.Port(t)
}
