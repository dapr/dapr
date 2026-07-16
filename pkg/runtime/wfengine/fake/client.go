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

	"github.com/dapr/components-contrib/workflows"
)

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

func NewClient() *FakeClient {
	return &FakeClient{
		initFn: func(metadata workflows.Metadata) error { return nil },
		startFn: func(ctx context.Context, req *workflows.StartRequest) (*workflows.StartResponse, error) {
			return new(workflows.StartResponse), nil
		},
		terminateFn: func(ctx context.Context, req *workflows.TerminateRequest) error { return nil },
		getFn: func(ctx context.Context, req *workflows.GetRequest) (*workflows.StateResponse, error) {
			return &workflows.StateResponse{
				Workflow: &workflows.WorkflowState{
					InstanceID: req.InstanceID,
					Properties: map[string]string{},
				},
			}, nil
		},
		raiseEventFn: func(ctx context.Context, req *workflows.RaiseEventRequest) error { return nil },
		purgeFn:      func(ctx context.Context, req *workflows.PurgeRequest) error { return nil },
		pauseFn:      func(ctx context.Context, req *workflows.PauseRequest) error { return nil },
		resumeFn:     func(ctx context.Context, req *workflows.ResumeRequest) error { return nil },
		closeFn:      func() error { return nil },
	}
}

func (f *FakeClient) WithInit(initFn func(metadata workflows.Metadata) error) *FakeClient {
	f.initFn = initFn
	return f
}

func (f *FakeClient) WithStart(startFn func(ctx context.Context, req *workflows.StartRequest) (*workflows.StartResponse, error)) *FakeClient {
	f.startFn = startFn
	return f
}

func (f *FakeClient) WithTerminate(terminateFn func(ctx context.Context, req *workflows.TerminateRequest) error) *FakeClient {
	f.terminateFn = terminateFn
	return f
}

func (f *FakeClient) WithGet(getFn func(ctx context.Context, req *workflows.GetRequest) (*workflows.StateResponse, error)) *FakeClient {
	f.getFn = getFn
	return f
}

func (f *FakeClient) WithRaiseEvent(raiseEventFn func(ctx context.Context, req *workflows.RaiseEventRequest) error) *FakeClient {
	f.raiseEventFn = raiseEventFn
	return f
}

func (f *FakeClient) WithPurge(purgeFn func(ctx context.Context, req *workflows.PurgeRequest) error) *FakeClient {
	f.purgeFn = purgeFn
	return f
}

func (f *FakeClient) WithPause(pauseFn func(ctx context.Context, req *workflows.PauseRequest) error) *FakeClient {
	f.pauseFn = pauseFn
	return f
}

func (f *FakeClient) WithResume(resumeFn func(ctx context.Context, req *workflows.ResumeRequest) error) *FakeClient {
	f.resumeFn = resumeFn
	return f
}

func (f *FakeClient) WithClose(closeFn func() error) *FakeClient {
	f.closeFn = closeFn
	return f
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
