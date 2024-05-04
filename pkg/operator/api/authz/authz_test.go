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
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dapr/dapr/pkg/security/spiffe"
	"github.com/dapr/dapr/tests/util"
)

func Test_Request(t *testing.T) {
	appID := spiffeid.RequireFromString("spiffe://example.org/ns/ns1/app1")
	serverID := spiffeid.RequireFromString("spiffe://example.org/ns/dapr-system/dapr-operator")
	pki := util.GenPKI(t, util.PKIOptions{LeafID: serverID, ClientID: appID})

	t.Run("no auth context should error", func(t *testing.T) {
		id, err := Request(context.Background(), "ns1")
		require.Error(t, err)
		assert.Equal(t, codes.PermissionDenied, status.Code(err))
		assert.Nil(t, id)
	})

	t.Run("different namespace should error", func(t *testing.T) {
		id, err := Request(pki.ClientGRPCCtx(t), "ns2")
		require.Error(t, err)
		assert.Equal(t, codes.PermissionDenied, status.Code(err))
		assert.Nil(t, id)
	})

	t.Run("empty namespace should error", func(t *testing.T) {
		id, err := Request(pki.ClientGRPCCtx(t), "")
		require.Error(t, err)
		assert.Equal(t, codes.PermissionDenied, status.Code(err))
		assert.Nil(t, id)
	})

	t.Run("invalid SPIFFE path should error", func(t *testing.T) {
		appID := spiffeid.RequireFromString("spiffe://example.org/foo/bar")
		pki2 := util.GenPKI(t, util.PKIOptions{LeafID: serverID, ClientID: appID})
		id, err := Request(pki2.ClientGRPCCtx(t), "ns1")
		require.Error(t, err)
		assert.Equal(t, codes.PermissionDenied, status.Code(err))
		assert.Nil(t, id)
	})

	t.Run("same namespace should no error", func(t *testing.T) {
		ctx := pki.ClientGRPCCtx(t)
		id, err := Request(ctx, "ns1")
		require.NoError(t, err)
		expID, err := spiffe.FromStrings(
			spiffeid.RequireTrustDomainFromString("example.org"),
			"ns1", "app1")
		require.NoError(t, err)
		assert.Equal(t, expID, id)
	})
}
