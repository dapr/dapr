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
			ctx: context.Background(),
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
			ctx: context.Background(),
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
			err := a.Initial(test.ctx, test.initial)
			assert.Equal(t, test.expCode != nil, err != nil, "%v %v", test.expCode, err)
			if test.expCode != nil {
				assert.Equal(t, *test.expCode, status.Code(err))
			}

			a = New(Options{fake.New().WithMTLSEnabled(false)})
			err = a.Initial(test.ctx, test.initial)
			assert.Equal(t, test.nonMTlSCode != nil, err != nil, "%v %v", test.nonMTlSCode, err)
			if test.nonMTlSCode != nil {
				assert.Equal(t, *test.nonMTlSCode, status.Code(err))
			}
		})
	}
}
