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

package authz

import (
	"context"
	"testing"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/security/fake"
	"github.com/dapr/kit/crypto/test"
	"github.com/dapr/kit/ptr"
)

func Test_Metadata(t *testing.T) {
	appID := spiffeid.RequireFromString("spiffe://example.org/ns/ns1/app1")
	serverID := spiffeid.RequireFromString("spiffe://example.org/ns/dapr-system/dapr-scheduler")
	pki := test.GenPKI(t, test.PKIOptions{LeafID: serverID, ClientID: appID})

	tests := map[string]struct {
		ctx         context.Context
		meta        *schedulerv1pb.JobMetadata
		expCode     *codes.Code
		nonMTlSCode *codes.Code
	}{
		"empty ns should error": {
			ctx: pki.ClientGRPCCtx(t),
			meta: &schedulerv1pb.JobMetadata{
				AppId:     "app1",
				Namespace: "",
			},
			expCode:     ptr.Of(codes.InvalidArgument),
			nonMTlSCode: ptr.Of(codes.InvalidArgument),
		},
		"empty appID should error": {
			ctx: pki.ClientGRPCCtx(t),
			meta: &schedulerv1pb.JobMetadata{
				AppId:     "",
				Namespace: "ns1",
			},
			expCode:     ptr.Of(codes.InvalidArgument),
			nonMTlSCode: ptr.Of(codes.InvalidArgument),
		},
		"no auth context should error": {
			ctx: t.Context(),
			meta: &schedulerv1pb.JobMetadata{
				AppId:     "app1",
				Namespace: "ns1",
			},
			expCode:     ptr.Of(codes.Unauthenticated),
			nonMTlSCode: nil,
		},
		"different namespace should error": {
			ctx: pki.ClientGRPCCtx(t),
			meta: &schedulerv1pb.JobMetadata{
				AppId:     "app1",
				Namespace: "ns2",
			},
			expCode:     ptr.Of(codes.PermissionDenied),
			nonMTlSCode: nil,
		},
		"different appID should error": {
			ctx: pki.ClientGRPCCtx(t),
			meta: &schedulerv1pb.JobMetadata{
				AppId:     "app2",
				Namespace: "ns1",
			},
			expCode:     ptr.Of(codes.PermissionDenied),
			nonMTlSCode: nil,
		},
		"valid request should pass": {
			ctx: pki.ClientGRPCCtx(t),
			meta: &schedulerv1pb.JobMetadata{
				AppId:     "app1",
				Namespace: "ns1",
			},
			expCode:     nil,
			nonMTlSCode: nil,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			a := New(Options{fake.New().WithMTLSEnabled(true)})
			err := a.Metadata(test.ctx, test.meta)
			assert.Equal(t, test.expCode != nil, err != nil, "%v %v", test.expCode, err)
			if test.expCode != nil {
				assert.Equal(t, *test.expCode, status.Code(err))
			}

			a = New(Options{fake.New().WithMTLSEnabled(false)})
			err = a.Metadata(test.ctx, test.meta)
			assert.Equal(t, test.nonMTlSCode != nil, err != nil, "%v %v", test.nonMTlSCode, err)
			if test.nonMTlSCode != nil {
				assert.Equal(t, *test.nonMTlSCode, status.Code(err))
			}
		})
	}
}

func Test_Initial(t *testing.T) {
	appID := spiffeid.RequireFromString("spiffe://example.org/ns/ns1/app1")
	serverID := spiffeid.RequireFromString("spiffe://example.org/ns/dapr-system/dapr-scheduler")
	pki := test.GenPKI(t, test.PKIOptions{LeafID: serverID, ClientID: appID})

	tests := map[string]struct {
		ctx         context.Context
		initial     *schedulerv1pb.WatchJobsRequestInitial
		expCode     *codes.Code
		nonMTlSCode *codes.Code
	}{
		"empty ns should error": {
			ctx: pki.ClientGRPCCtx(t),
			initial: &schedulerv1pb.WatchJobsRequestInitial{
				AppId:     "app1",
				Namespace: "",
			},
			expCode:     ptr.Of(codes.InvalidArgument),
			nonMTlSCode: ptr.Of(codes.InvalidArgument),
		},
		"empty appID should error": {
			ctx: pki.ClientGRPCCtx(t),
			initial: &schedulerv1pb.WatchJobsRequestInitial{
				AppId:     "",
				Namespace: "ns1",
			},
			expCode:     ptr.Of(codes.InvalidArgument),
			nonMTlSCode: ptr.Of(codes.InvalidArgument),
		},
		"no auth context should error": {
			ctx: t.Context(),
			initial: &schedulerv1pb.WatchJobsRequestInitial{
				AppId:     "app1",
				Namespace: "ns1",
			},
			expCode:     ptr.Of(codes.Unauthenticated),
			nonMTlSCode: nil,
		},
		"different namespace should error": {
			ctx: pki.ClientGRPCCtx(t),
			initial: &schedulerv1pb.WatchJobsRequestInitial{
				AppId:     "app1",
				Namespace: "ns2",
			},
			expCode:     ptr.Of(codes.PermissionDenied),
			nonMTlSCode: nil,
		},
		"different appID should error": {
			ctx: pki.ClientGRPCCtx(t),
			initial: &schedulerv1pb.WatchJobsRequestInitial{
				AppId:     "app2",
				Namespace: "ns1",
			},
			expCode:     ptr.Of(codes.PermissionDenied),
			nonMTlSCode: nil,
		},
		"valid request should pass": {
			ctx: pki.ClientGRPCCtx(t),
			initial: &schedulerv1pb.WatchJobsRequestInitial{
				AppId:     "app1",
				Namespace: "ns1",
			},
			expCode:     nil,
			nonMTlSCode: nil,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			a := New(Options{fake.New().WithMTLSEnabled(true)})
			err := a.WatchInitial(test.ctx, test.initial)
			assert.Equal(t, test.expCode != nil, err != nil, "%v %v", test.expCode, err)
			if test.expCode != nil {
				assert.Equal(t, *test.expCode, status.Code(err))
			}

			a = New(Options{fake.New().WithMTLSEnabled(false)})
			err = a.WatchInitial(test.ctx, test.initial)
			assert.Equal(t, test.nonMTlSCode != nil, err != nil, "%v %v", test.nonMTlSCode, err)
			if test.nonMTlSCode != nil {
				assert.Equal(t, *test.nonMTlSCode, status.Code(err))
			}
		})
	}
}

func Test_Job(t *testing.T) {
	appID := spiffeid.RequireFromString("spiffe://example.org/ns/ns1/app1")
	serverID := spiffeid.RequireFromString("spiffe://example.org/ns/dapr-system/dapr-scheduler")
	pki := test.GenPKI(t, test.PKIOptions{LeafID: serverID, ClientID: appID})

	actorMeta := func(actorType string) *schedulerv1pb.JobMetadata {
		return &schedulerv1pb.JobMetadata{
			AppId:     "app1",
			Namespace: "ns1",
			Target: &schedulerv1pb.JobTargetMetadata{
				Type: &schedulerv1pb.JobTargetMetadata_Actor{
					Actor: &schedulerv1pb.TargetActorReminder{Id: "id", Type: actorType},
				},
			},
		}
	}
	schedule := func(name string, meta *schedulerv1pb.JobMetadata) Request {
		return &schedulerv1pb.ScheduleJobRequest{Name: name, Metadata: meta}
	}
	overwrite := func(name string, meta *schedulerv1pb.JobMetadata) Request {
		return &schedulerv1pb.ScheduleJobRequest{Name: name, Metadata: meta, Overwrite: true}
	}
	get := func(name string, meta *schedulerv1pb.JobMetadata) Request {
		return &schedulerv1pb.GetJobRequest{Name: name, Metadata: meta}
	}
	del := func(name string, meta *schedulerv1pb.JobMetadata) Request {
		return &schedulerv1pb.DeleteJobRequest{Name: name, Metadata: meta}
	}

	tests := map[string]struct {
		req     Request
		expCode *codes.Code
	}{
		"job target is not restricted": {
			req: schedule("job", &schedulerv1pb.JobMetadata{
				AppId:     "app1",
				Namespace: "ns1",
				Target: &schedulerv1pb.JobTargetMetadata{
					Type: &schedulerv1pb.JobTargetMetadata_Job{Job: new(schedulerv1pb.TargetJob)},
				},
			}),
		},
		"user actor type hosted by another app is allowed": {
			req: schedule("rem", actorMeta("someone-elses-type")),
		},
		"own internal workflow type is allowed": {
			req: schedule("new-event", actorMeta("dapr.internal.ns1.app1.workflow")),
		},
		"own internal activity type is allowed": {
			req: schedule("run-activity", actorMeta("dapr.internal.ns1.app1.activity")),
		},
		"own activity result is allowed to schedule with overwrite": {
			req: overwrite("activity-result-abc", actorMeta("dapr.internal.ns1.app1.workflow")),
		},
		"own activity result is allowed to get": {
			req: get("activity-result-abc", actorMeta("dapr.internal.ns1.app1.workflow")),
		},
		"own activity result is allowed to delete": {
			req: del("activity-result-abc", actorMeta("dapr.internal.ns1.app1.workflow")),
		},
		"other app internal workflow type is denied": {
			req:     schedule("new-event", actorMeta("dapr.internal.ns1.app2.workflow")),
			expCode: ptr.Of(codes.PermissionDenied),
		},
		"other app internal activity type is denied": {
			req:     schedule("run-activity", actorMeta("dapr.internal.ns1.app2.activity")),
			expCode: ptr.Of(codes.PermissionDenied),
		},
		"other namespace internal type is denied": {
			req:     schedule("new-event", actorMeta("dapr.internal.ns2.app1.workflow")),
			expCode: ptr.Of(codes.PermissionDenied),
		},
		"malformed internal type is denied": {
			req:     schedule("new-event", actorMeta("dapr.internal.ns1")),
			expCode: ptr.Of(codes.PermissionDenied),
		},
		"activity result on other app workflow is allowed to schedule without overwrite": {
			req: schedule("activity-result-abc.exec1", actorMeta("dapr.internal.ns1.app2.workflow")),
		},
		"activity result on other app workflow is denied to schedule with overwrite": {
			req:     overwrite("activity-result-abc.exec1", actorMeta("dapr.internal.ns1.app2.workflow")),
			expCode: ptr.Of(codes.PermissionDenied),
		},
		"activity result on other app workflow is denied to get": {
			req:     get("activity-result-abc.exec1", actorMeta("dapr.internal.ns1.app2.workflow")),
			expCode: ptr.Of(codes.PermissionDenied),
		},
		"activity result on other app workflow is denied to delete": {
			req:     del("activity-result-abc.exec1", actorMeta("dapr.internal.ns1.app2.workflow")),
			expCode: ptr.Of(codes.PermissionDenied),
		},
		"activity result on other namespace workflow is denied": {
			req:     schedule("activity-result-abc", actorMeta("dapr.internal.ns2.app2.workflow")),
			expCode: ptr.Of(codes.PermissionDenied),
		},
		"activity result on other app activity type is denied": {
			req:     schedule("activity-result-abc", actorMeta("dapr.internal.ns1.app2.activity")),
			expCode: ptr.Of(codes.PermissionDenied),
		},
		"unnamed request on other app internal type is denied": {
			req:     schedule("", actorMeta("dapr.internal.ns1.app2.workflow")),
			expCode: ptr.Of(codes.PermissionDenied),
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			for _, mtls := range []bool{true, false} {
				a := New(Options{fake.New().WithMTLSEnabled(mtls)})
				err := a.Job(pki.ClientGRPCCtx(t), test.req)
				assert.Equal(t, test.expCode != nil, err != nil, "mtls=%t %v %v", mtls, test.expCode, err)
				if test.expCode != nil {
					assert.Equal(t, *test.expCode, status.Code(err))
				}
			}
		})
	}

	t.Run("metadata only requests never get the activity result exemption", func(t *testing.T) {
		a := New(Options{fake.New().WithMTLSEnabled(true)})
		err := a.Metadata(pki.ClientGRPCCtx(t), actorMeta("dapr.internal.ns1.app2.workflow"))
		assert.Equal(t, codes.PermissionDenied, status.Code(err))
	})
}
