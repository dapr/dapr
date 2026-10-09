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

package grpc

import (
	"context"
	"testing"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	workflowacl "github.com/dapr/dapr/pkg/acl/workflow"
	"github.com/dapr/dapr/pkg/api/universal"
	wfaclapi "github.com/dapr/dapr/pkg/apis/workflowaccesspolicy/v1alpha1"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/kit/crypto/test"
	"github.com/dapr/kit/logger"
)

func TestCallActorInternalActorTypeSameApp(t *testing.T) {
	serverID := spiffeid.RequireFromString("spiffe://example.org/ns/ns1/app1")
	callerCtx := func(id string) context.Context {
		return test.GenPKI(t, test.PKIOptions{
			LeafID:   serverID,
			ClientID: spiffeid.RequireFromString(id),
		}).ClientGRPCCtx(t)
	}

	a := &api{
		logger: logger.NewLogger("test.api.grpc.workflowacl"),
		Universal: universal.New(universal.Options{
			AppID:     "app1",
			Namespace: "ns1",
		}),
	}

	sameApp := callerCtx("spiffe://example.org/ns/ns1/app1")
	otherApp := callerCtx("spiffe://example.org/ns/ns1/app2")
	otherNamespace := callerCtx("spiffe://example.org/ns/ns2/app1")

	tests := map[string]struct {
		ctx       context.Context
		actorType string
		expCode   codes.Code
		expMsg    string
	}{
		"executor: same app is allowed":          {ctx: sameApp, actorType: "dapr.internal.ns1.app1.executor", expCode: codes.OK},
		"executor: other app is denied":          {ctx: otherApp, actorType: "dapr.internal.ns1.app1.executor", expCode: codes.PermissionDenied, expMsg: "reserved internal actor type 'dapr.internal.ns1.app1.executor' can only be called by a sidecar with the same app ID and namespace"},
		"executor: other namespace is denied":    {ctx: otherNamespace, actorType: "dapr.internal.ns1.app1.executor", expCode: codes.PermissionDenied, expMsg: "access denied by workflow access policy"},
		"executor: no identity is allowed":       {ctx: t.Context(), actorType: "dapr.internal.ns1.app1.executor", expCode: codes.OK},
		"retentioner: same app is allowed":       {ctx: sameApp, actorType: "dapr.internal.ns1.app1.retentioner", expCode: codes.OK},
		"retentioner: other app is denied":       {ctx: otherApp, actorType: "dapr.internal.ns1.app1.retentioner", expCode: codes.PermissionDenied, expMsg: "reserved internal actor type 'dapr.internal.ns1.app1.retentioner' can only be called by a sidecar with the same app ID and namespace"},
		"retentioner: other namespace is denied": {ctx: otherNamespace, actorType: "dapr.internal.ns1.app1.retentioner", expCode: codes.PermissionDenied, expMsg: "access denied by workflow access policy"},
		"retentioner: no identity is allowed":    {ctx: t.Context(), actorType: "dapr.internal.ns1.app1.retentioner", expCode: codes.OK},
		"user actor type is not checked":         {ctx: otherApp, actorType: "myactortype", expCode: codes.OK},
	}

	t.Run("no identity is denied when a policy is loaded", func(t *testing.T) {
		withPolicy := &api{
			logger:                 a.logger,
			Universal:              a.Universal,
			workflowAccessPolicies: workflowacl.NewHolder(),
		}
		withPolicy.workflowAccessPolicies.Store(workflowacl.Compile([]wfaclapi.WorkflowAccessPolicy{{}}))
		require.NotNil(t, withPolicy.workflowAccessPolicies.Load())

		for _, actorType := range []string{"dapr.internal.ns1.app1.executor", "dapr.internal.ns1.app1.retentioner"} {
			err := withPolicy.callActorValidateWorkflowACL(t.Context(),
				internalv1pb.NewInternalInvokeRequest("Complete").WithActor(actorType, "id"),
			)
			assert.Equal(t, codes.PermissionDenied, status.Code(err), "CallActor %s: %v", actorType, err)

			err = withPolicy.callActorReminderValidateWorkflowACL(t.Context(), &internalv1pb.Reminder{
				ActorType: actorType,
				ActorId:   "id",
				Name:      "reminder",
			})
			assert.Equal(t, codes.PermissionDenied, status.Code(err), "CallActorReminder %s: %v", actorType, err)
		}
	})

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			err := a.callActorValidateWorkflowACL(tc.ctx,
				internalv1pb.NewInternalInvokeRequest("Complete").WithActor(tc.actorType, "id"),
			)
			assert.Equal(t, tc.expCode, status.Code(err), "CallActor: %v", err)
			if tc.expMsg != "" {
				require.ErrorContains(t, err, tc.expMsg)
			}

			err = a.callActorReminderValidateWorkflowACL(tc.ctx, &internalv1pb.Reminder{
				ActorType: tc.actorType,
				ActorId:   "id",
				Name:      "reminder",
			})
			assert.Equal(t, tc.expCode, status.Code(err), "CallActorReminder: %v", err)
			if tc.expMsg != "" {
				require.ErrorContains(t, err, tc.expMsg)
			}
		})
	}
}
