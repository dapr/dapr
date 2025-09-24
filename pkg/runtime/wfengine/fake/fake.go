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
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
)

type Fake struct {
	runFn                func(context.Context) error
	initFn               func() error
	registerGrpcServerFn func(*grpc.Server)
	waitForReadyFn       func(context.Context) error
	clientFn             func() workflows.Workflow
	runtimeMetadataFn    func() *runtimev1pb.MetadataWorkflows
}

func New() *Fake {
	return &Fake{
		runFn:                func(context.Context) error { return nil },
		initFn:               func() error { return nil },
		registerGrpcServerFn: func(*grpc.Server) {},
		waitForReadyFn:       func(context.Context) error { return nil },
		clientFn:             func() workflows.Workflow { return NewClient() },
		runtimeMetadataFn:    func() *runtimev1pb.MetadataWorkflows { return &runtimev1pb.MetadataWorkflows{} },
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

func (f *Fake) WithRuntimeMetadata(runtimeMetadataFn func() *runtimev1pb.MetadataWorkflows) *Fake {
	f.runtimeMetadataFn = runtimeMetadataFn
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

func (f *Fake) ActivityActorType() string {
	return ""
}

func (f *Fake) RuntimeMetadata() *runtimev1pb.MetadataWorkflows {
	return f.runtimeMetadataFn()
}
