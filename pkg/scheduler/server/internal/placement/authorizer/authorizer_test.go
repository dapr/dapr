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

package authorizer

import (
	"context"
	"testing"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/security/fake"
	"github.com/dapr/kit/crypto/test"
	"github.com/dapr/kit/ptr"
)

func Test_Host(t *testing.T) {
	appID := spiffeid.RequireFromString("spiffe://example.org/ns/ns1/app1")
	serverID := spiffeid.RequireFromString("spiffe://example.org/ns/dapr-system/dapr-scheduler")
	pki := test.GenPKI(t, test.PKIOptions{LeafID: serverID, ClientID: appID})

	tests := map[string]struct {
		ctx context.Context
		msg *schedulerv1pb.ActorHost
		// expCode is the expected gRPC code with mTLS enabled, nil for no
		// error.
		expCode *codes.Code
		// nonMTLSCode is the expected gRPC code with mTLS disabled, where
		// there is no client identity to validate the report against.
		nonMTLSCode *codes.Code
	}{
		"nil report should error": {
			ctx:         pki.ClientGRPCCtx(t),
			msg:         nil,
			expCode:     ptr.Of(codes.InvalidArgument),
			nonMTLSCode: ptr.Of(codes.InvalidArgument),
		},
		"empty address should error": {
			ctx: pki.ClientGRPCCtx(t),
			msg: &schedulerv1pb.ActorHost{
				Address: "", AppId: "app1", Namespace: "ns1",
			},
			expCode:     ptr.Of(codes.InvalidArgument),
			nonMTLSCode: ptr.Of(codes.InvalidArgument),
		},
		"empty appID should error": {
			ctx: pki.ClientGRPCCtx(t),
			msg: &schedulerv1pb.ActorHost{
				Address: "10.0.0.1:50002", AppId: "", Namespace: "ns1",
			},
			expCode:     ptr.Of(codes.InvalidArgument),
			nonMTLSCode: ptr.Of(codes.InvalidArgument),
		},
		"empty namespace should error": {
			ctx: pki.ClientGRPCCtx(t),
			msg: &schedulerv1pb.ActorHost{
				Address: "10.0.0.1:50002", AppId: "app1", Namespace: "",
			},
			expCode:     ptr.Of(codes.InvalidArgument),
			nonMTLSCode: ptr.Of(codes.InvalidArgument),
		},
		"no auth context should error under mTLS": {
			ctx: t.Context(),
			msg: &schedulerv1pb.ActorHost{
				Address: "10.0.0.1:50002", AppId: "app1", Namespace: "ns1",
			},
			expCode:     ptr.Of(codes.Unauthenticated),
			nonMTLSCode: nil,
		},
		"different appID should error": {
			ctx: pki.ClientGRPCCtx(t),
			msg: &schedulerv1pb.ActorHost{
				Address: "10.0.0.1:50002", AppId: "app2", Namespace: "ns1",
			},
			expCode: ptr.Of(codes.PermissionDenied),
			// Without mTLS there is no identity to contradict the report.
			nonMTLSCode: nil,
		},
		"different namespace should error": {
			ctx: pki.ClientGRPCCtx(t),
			msg: &schedulerv1pb.ActorHost{
				Address: "10.0.0.1:50002", AppId: "app1", Namespace: "ns2",
			},
			expCode:     ptr.Of(codes.PermissionDenied),
			nonMTLSCode: nil,
		},
		"valid report should pass": {
			ctx: pki.ClientGRPCCtx(t),
			msg: &schedulerv1pb.ActorHost{
				Address: "10.0.0.1:50002", AppId: "app1", Namespace: "ns1",
			},
			expCode:     nil,
			nonMTLSCode: nil,
		},
		"valid report hosting no actor types should pass": {
			ctx: pki.ClientGRPCCtx(t),
			msg: &schedulerv1pb.ActorHost{
				Address: "10.0.0.1:50002", AppId: "app1", Namespace: "ns1",
				ActorTypes: nil,
			},
			expCode:     nil,
			nonMTLSCode: nil,
		},
		"valid report hosting actor types should pass": {
			ctx: pki.ClientGRPCCtx(t),
			msg: &schedulerv1pb.ActorHost{
				Address: "10.0.0.1:50002", AppId: "app1", Namespace: "ns1",
				ActorTypes: []string{"myactor", "dapr.internal.ns1.app1.workflow"},
			},
			expCode:     nil,
			nonMTLSCode: nil,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			a := New(Options{Security: fake.New().WithMTLSEnabled(true)})
			err := a.Host(test.ctx, test.msg)
			assert.Equal(t, test.expCode != nil, err != nil, "%v %v", test.expCode, err)
			if test.expCode != nil {
				assert.Equal(t, *test.expCode, status.Code(err))
			}

			a = New(Options{Security: fake.New().WithMTLSEnabled(false)})
			err = a.Host(test.ctx, test.msg)
			assert.Equal(t, test.nonMTLSCode != nil, err != nil, "%v %v", test.nonMTLSCode, err)
			if test.nonMTLSCode != nil {
				assert.Equal(t, *test.nonMTLSCode, status.Code(err))
			}
		})
	}
}

// Test_ActorTypes covers the internal actor type namespacing rule: a host may
// only claim dapr.internal.<namespace>.<appid>.* types which belong to itself.
// This runs regardless of mTLS, since it compares the claimed types against
// the namespace and app ID on the same report.
func Test_ActorTypes(t *testing.T) {
	tests := map[string]struct {
		actorTypes []string
		expErr     bool
	}{
		"no types is allowed": {
			actorTypes: nil,
			expErr:     false,
		},
		"ordinary user types are allowed": {
			actorTypes: []string{"myactor", "OtherActor", "a.b.c.d"},
			expErr:     false,
		},
		"own internal type is allowed": {
			actorTypes: []string{"dapr.internal.ns1.app1.workflow"},
			expErr:     false,
		},
		"own internal type with extra segments is allowed": {
			actorTypes: []string{"dapr.internal.ns1.app1.activity.sub"},
			expErr:     false,
		},
		"own internal type with exactly four segments is allowed": {
			actorTypes: []string{"dapr.internal.ns1.app1"},
			expErr:     false,
		},
		"internal type of another app is denied": {
			actorTypes: []string{"dapr.internal.ns1.app2.workflow"},
			expErr:     true,
		},
		"internal type of another namespace is denied": {
			actorTypes: []string{"dapr.internal.ns2.app1.workflow"},
			expErr:     true,
		},
		"truncated internal type is denied": {
			actorTypes: []string{"dapr.internal"},
			expErr:     true,
		},
		"internal type missing the app ID is denied": {
			actorTypes: []string{"dapr.internal.ns1"},
			expErr:     true,
		},
		"a denied type anywhere in the list denies the report": {
			actorTypes: []string{"myactor", "dapr.internal.ns1.app1.workflow", "dapr.internal.ns9.app9.x"},
			expErr:     true,
		},
		"a type merely prefixed with dapr is allowed": {
			actorTypes: []string{"dapr.something.ns2.app2", "daprinternal.ns2.app2"},
			expErr:     false,
		},
		"a type whose second segment only starts with internal is allowed": {
			actorTypes: []string{"dapr.internalfoo.ns2.app2"},
			expErr:     false,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			a := New(Options{Security: fake.New().WithMTLSEnabled(false)})

			err := a.Host(t.Context(), &schedulerv1pb.ActorHost{
				Address:    "10.0.0.1:50002",
				AppId:      "app1",
				Namespace:  "ns1",
				ActorTypes: test.actorTypes,
			})

			assert.Equal(t, test.expErr, err != nil, "%v", err)
			if test.expErr {
				assert.Equal(t, codes.PermissionDenied, status.Code(err))
			}
		})
	}
}

// Test_HostEnforcesActorTypesUnderMTLS asserts the internal actor type rule is
// reached through Host, after the identity check, so a host with a valid
// SPIFFE identity still cannot claim another app's internal types.
func Test_HostEnforcesActorTypesUnderMTLS(t *testing.T) {
	appID := spiffeid.RequireFromString("spiffe://example.org/ns/ns1/app1")
	serverID := spiffeid.RequireFromString("spiffe://example.org/ns/dapr-system/dapr-scheduler")
	pki := test.GenPKI(t, test.PKIOptions{LeafID: serverID, ClientID: appID})

	a := New(Options{Security: fake.New().WithMTLSEnabled(true)})

	err := a.Host(pki.ClientGRPCCtx(t), &schedulerv1pb.ActorHost{
		Address:    "10.0.0.1:50002",
		AppId:      "app1",
		Namespace:  "ns1",
		ActorTypes: []string{"dapr.internal.ns1.app2.workflow"},
	})

	require.Error(t, err)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
}
