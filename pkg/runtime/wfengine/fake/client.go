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

	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/backend"
)

type FakeClient struct {
	scheduleNewWorkflowFn       func(ctx context.Context, workflow any, opts ...api.NewWorkflowOptions) (api.InstanceID, error)
	fetchWorkflowMetadataFn     func(ctx context.Context, id api.InstanceID, opts ...api.FetchWorkflowMetadataOptions) (*backend.WorkflowMetadata, error)
	waitForWorkflowStartFn      func(ctx context.Context, id api.InstanceID, opts ...api.FetchWorkflowMetadataOptions) (*backend.WorkflowMetadata, error)
	waitForWorkflowCompletionFn func(ctx context.Context, id api.InstanceID, opts ...api.FetchWorkflowMetadataOptions) (*backend.WorkflowMetadata, error)
	terminateWorkflowFn         func(ctx context.Context, id api.InstanceID, opts ...api.TerminateOptions) error
	raiseEventFn                func(ctx context.Context, id api.InstanceID, eventName string, opts ...api.RaiseEventOptions) error
	suspendWorkflowFn           func(ctx context.Context, id api.InstanceID, reason string, opts ...api.SuspendOptions) error
	resumeWorkflowFn            func(ctx context.Context, id api.InstanceID, reason string, opts ...api.ResumeOptions) error
	purgeWorkflowStateFn        func(ctx context.Context, id api.InstanceID, opts ...api.PurgeOptions) error
	rerunWorkflowFromEventFn    func(ctx context.Context, source api.InstanceID, eventID uint32, opts ...api.RerunOptions) (api.InstanceID, error)
}

func NewClient() *FakeClient {
	return &FakeClient{
		scheduleNewWorkflowFn: func(ctx context.Context, workflow any, opts ...api.NewWorkflowOptions) (api.InstanceID, error) {
			return api.EmptyInstanceID, nil
		},
		fetchWorkflowMetadataFn: func(ctx context.Context, id api.InstanceID, opts ...api.FetchWorkflowMetadataOptions) (*backend.WorkflowMetadata, error) {
			return &backend.WorkflowMetadata{InstanceId: string(id)}, nil
		},
		waitForWorkflowStartFn: func(ctx context.Context, id api.InstanceID, opts ...api.FetchWorkflowMetadataOptions) (*backend.WorkflowMetadata, error) {
			return &backend.WorkflowMetadata{InstanceId: string(id)}, nil
		},
		waitForWorkflowCompletionFn: func(ctx context.Context, id api.InstanceID, opts ...api.FetchWorkflowMetadataOptions) (*backend.WorkflowMetadata, error) {
			return &backend.WorkflowMetadata{InstanceId: string(id)}, nil
		},
		terminateWorkflowFn: func(ctx context.Context, id api.InstanceID, opts ...api.TerminateOptions) error { return nil },
		raiseEventFn: func(ctx context.Context, id api.InstanceID, eventName string, opts ...api.RaiseEventOptions) error {
			return nil
		},
		suspendWorkflowFn: func(ctx context.Context, id api.InstanceID, reason string, opts ...api.SuspendOptions) error {
			return nil
		},
		resumeWorkflowFn: func(ctx context.Context, id api.InstanceID, reason string, opts ...api.ResumeOptions) error {
			return nil
		},
		purgeWorkflowStateFn: func(ctx context.Context, id api.InstanceID, opts ...api.PurgeOptions) error { return nil },
		rerunWorkflowFromEventFn: func(ctx context.Context, source api.InstanceID, eventID uint32, opts ...api.RerunOptions) (api.InstanceID, error) {
			return api.EmptyInstanceID, nil
		},
	}
}

func (f *FakeClient) WithScheduleNewWorkflow(fn func(ctx context.Context, workflow any, opts ...api.NewWorkflowOptions) (api.InstanceID, error)) *FakeClient {
	f.scheduleNewWorkflowFn = fn
	return f
}

func (f *FakeClient) WithFetchWorkflowMetadata(fn func(ctx context.Context, id api.InstanceID, opts ...api.FetchWorkflowMetadataOptions) (*backend.WorkflowMetadata, error)) *FakeClient {
	f.fetchWorkflowMetadataFn = fn
	return f
}

func (f *FakeClient) WithWaitForWorkflowStart(fn func(ctx context.Context, id api.InstanceID, opts ...api.FetchWorkflowMetadataOptions) (*backend.WorkflowMetadata, error)) *FakeClient {
	f.waitForWorkflowStartFn = fn
	return f
}

func (f *FakeClient) WithWaitForWorkflowCompletion(fn func(ctx context.Context, id api.InstanceID, opts ...api.FetchWorkflowMetadataOptions) (*backend.WorkflowMetadata, error)) *FakeClient {
	f.waitForWorkflowCompletionFn = fn
	return f
}

func (f *FakeClient) WithTerminateWorkflow(fn func(ctx context.Context, id api.InstanceID, opts ...api.TerminateOptions) error) *FakeClient {
	f.terminateWorkflowFn = fn
	return f
}

func (f *FakeClient) WithRaiseEvent(fn func(ctx context.Context, id api.InstanceID, eventName string, opts ...api.RaiseEventOptions) error) *FakeClient {
	f.raiseEventFn = fn
	return f
}

func (f *FakeClient) WithSuspendWorkflow(fn func(ctx context.Context, id api.InstanceID, reason string, opts ...api.SuspendOptions) error) *FakeClient {
	f.suspendWorkflowFn = fn
	return f
}

func (f *FakeClient) WithResumeWorkflow(fn func(ctx context.Context, id api.InstanceID, reason string, opts ...api.ResumeOptions) error) *FakeClient {
	f.resumeWorkflowFn = fn
	return f
}

func (f *FakeClient) WithPurgeWorkflowState(fn func(ctx context.Context, id api.InstanceID, opts ...api.PurgeOptions) error) *FakeClient {
	f.purgeWorkflowStateFn = fn
	return f
}

func (f *FakeClient) WithRerunWorkflowFromEvent(fn func(ctx context.Context, source api.InstanceID, eventID uint32, opts ...api.RerunOptions) (api.InstanceID, error)) *FakeClient {
	f.rerunWorkflowFromEventFn = fn
	return f
}

func (f *FakeClient) ScheduleNewWorkflow(ctx context.Context, workflow any, opts ...api.NewWorkflowOptions) (api.InstanceID, error) {
	return f.scheduleNewWorkflowFn(ctx, workflow, opts...)
}

func (f *FakeClient) FetchWorkflowMetadata(ctx context.Context, id api.InstanceID, opts ...api.FetchWorkflowMetadataOptions) (*backend.WorkflowMetadata, error) {
	return f.fetchWorkflowMetadataFn(ctx, id, opts...)
}

func (f *FakeClient) WaitForWorkflowStart(ctx context.Context, id api.InstanceID, opts ...api.FetchWorkflowMetadataOptions) (*backend.WorkflowMetadata, error) {
	return f.waitForWorkflowStartFn(ctx, id, opts...)
}

func (f *FakeClient) WaitForWorkflowCompletion(ctx context.Context, id api.InstanceID, opts ...api.FetchWorkflowMetadataOptions) (*backend.WorkflowMetadata, error) {
	return f.waitForWorkflowCompletionFn(ctx, id, opts...)
}

func (f *FakeClient) TerminateWorkflow(ctx context.Context, id api.InstanceID, opts ...api.TerminateOptions) error {
	return f.terminateWorkflowFn(ctx, id, opts...)
}

func (f *FakeClient) RaiseEvent(ctx context.Context, id api.InstanceID, eventName string, opts ...api.RaiseEventOptions) error {
	return f.raiseEventFn(ctx, id, eventName, opts...)
}

func (f *FakeClient) SuspendWorkflow(ctx context.Context, id api.InstanceID, reason string, opts ...api.SuspendOptions) error {
	return f.suspendWorkflowFn(ctx, id, reason, opts...)
}

func (f *FakeClient) ResumeWorkflow(ctx context.Context, id api.InstanceID, reason string, opts ...api.ResumeOptions) error {
	return f.resumeWorkflowFn(ctx, id, reason, opts...)
}

func (f *FakeClient) PurgeWorkflowState(ctx context.Context, id api.InstanceID, opts ...api.PurgeOptions) error {
	return f.purgeWorkflowStateFn(ctx, id, opts...)
}

func (f *FakeClient) RerunWorkflowFromEvent(ctx context.Context, source api.InstanceID, eventID uint32, opts ...api.RerunOptions) (api.InstanceID, error) {
	return f.rerunWorkflowFromEventFn(ctx, source, eventID, opts...)
}
