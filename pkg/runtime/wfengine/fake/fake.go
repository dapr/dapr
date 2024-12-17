/*
Copyright 2024 The Dapr Authors
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
	"context"

	"google.golang.org/grpc"

	"github.com/dapr/components-contrib/workflows"
)

type Fake struct {
	runFn                func(context.Context) error
	initFn               func() error
	registerGrpcServerFn func(*grpc.Server)
	waitForReadyFn       func(context.Context) error
	clientFn             func() workflows.Workflow
}

type FakeClient struct {
	initFn       func(metadata workflows.Metadata) error
	startFn      func(ctx context.Context, req *workflows.StartRequest) (*workflows.StartResponse, error)
	terminateFn  func(ctx context.Context, req *workflows.TerminateRequest) error
	getFn        func(ctx context.Context, req *workflows.GetRequest) (*workflows.StateResponse, error)
	raiseEventFn func(ctx context.Context, req *workflows.RaiseEventRequest) error
	purgeFn      func(ctx context.Context, req *workflows.PurgeRequest) error
	pauseFn      func(ctx context.Context, req *workflows.PauseRequest) error
	resumeFn     func(ctx context.Context, req *workflows.ResumeRequest) error
	closeFn      func() error
}

func New() *Fake {
	return &Fake{
		runFn:                func(context.Context) error { return nil },
		initFn:               func() error { return nil },
		registerGrpcServerFn: func(*grpc.Server) {},
		waitForReadyFn:       func(context.Context) error { return nil },
		clientFn: func() workflows.Workflow {
			return &FakeClient{
				initFn: func(metadata workflows.Metadata) error { return nil },
				startFn: func(ctx context.Context, req *workflows.StartRequest) (*workflows.StartResponse, error) {
					return new(workflows.StartResponse), nil
				},
				terminateFn: func(ctx context.Context, req *workflows.TerminateRequest) error { return nil },
				getFn: func(ctx context.Context, req *workflows.GetRequest) (*workflows.StateResponse, error) {
					return &workflows.StateResponse{
						Workflow: new(workflows.WorkflowState),
					}, nil
				},
				raiseEventFn: func(ctx context.Context, req *workflows.RaiseEventRequest) error { return nil },
				purgeFn:      func(ctx context.Context, req *workflows.PurgeRequest) error { return nil },
				pauseFn:      func(ctx context.Context, req *workflows.PauseRequest) error { return nil },
				resumeFn:     func(ctx context.Context, req *workflows.ResumeRequest) error { return nil },
				closeFn:      func() error { return nil },
			}
		},
	}
}

func (f *Fake) WithRun(runFn func(ctx context.Context) error) *Fake {
	f.runFn = runFn
	return f
}

func (f *Fake) WithInit(initFn func() error) *Fake {
	f.initFn = initFn
	return f
}

func (f *Fake) WithRegisterGrpcServer(registerGrpcServerFn func(grpcServer *grpc.Server)) *Fake {
	f.registerGrpcServerFn = registerGrpcServerFn
	return f
}

func (f *Fake) WithWaitForReady(waitForReadyFn func(ctx context.Context) error) *Fake {
	f.waitForReadyFn = waitForReadyFn
	return f
}

func (f *Fake) WithClient(clientFn func() workflows.Workflow) *Fake {
	f.clientFn = clientFn
	return f
}

func (f *Fake) Run(ctx context.Context) error {
	return f.runFn(ctx)
}

func (f *Fake) Init() error {
	return f.initFn()
}

func (f *Fake) RegisterGrpcServer(grpcServer *grpc.Server) {
	f.registerGrpcServerFn(grpcServer)
}

func (f *Fake) WaitForReady(ctx context.Context) error {
	return f.waitForReadyFn(ctx)
}

func (f *Fake) Client() workflows.Workflow {
	return f.clientFn()
}

func (f *FakeClient) Init(metadata workflows.Metadata) error {
	return f.initFn(metadata)
}

func (f *FakeClient) Start(ctx context.Context, req *workflows.StartRequest) (*workflows.StartResponse, error) {
	return f.startFn(ctx, req)
}

func (f *FakeClient) Terminate(ctx context.Context, req *workflows.TerminateRequest) error {
	return f.terminateFn(ctx, req)
}

func (f *FakeClient) Get(ctx context.Context, req *workflows.GetRequest) (*workflows.StateResponse, error) {
	return f.getFn(ctx, req)
}

func (f *FakeClient) RaiseEvent(ctx context.Context, req *workflows.RaiseEventRequest) error {
	return f.raiseEventFn(ctx, req)
}

func (f *FakeClient) Purge(ctx context.Context, req *workflows.PurgeRequest) error {
	return f.purgeFn(ctx, req)
}

func (f *FakeClient) Pause(ctx context.Context, req *workflows.PauseRequest) error {
	return f.pauseFn(ctx, req)
}

func (f *FakeClient) Resume(ctx context.Context, req *workflows.ResumeRequest) error {
	return f.resumeFn(ctx, req)
}

func (f *FakeClient) Close() error {
	return f.closeFn()
}
