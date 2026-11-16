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

package accesscontrol

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dapr/dapr/pkg/healthz"
	commonv1 "github.com/dapr/dapr/pkg/proto/common/v1"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(httpverb))
}

// httpverb verifies that a verb-specific ACL rule is applied to a CallLocal
// request from a mesh peer even when the request carries no metadata.
type httpverb struct {
	sentry *sentry.Sentry
	daprd  *daprd.Daprd

	lock sync.Mutex
	hits map[string]int
}

const httpverbPeerAppID = "httpverb-peer"

func (h *httpverb) Setup(t *testing.T) []framework.Option {
	h.hits = make(map[string]int)
	app := prochttp.New(t, prochttp.WithHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.lock.Lock()
		h.hits[r.Method+" "+r.URL.Path]++
		h.lock.Unlock()
	})))

	h.sentry = sentry.New(t)
	h.daprd = daprd.New(t,
		daprd.WithSentry(t, h.sentry),
		daprd.WithAppPort(app.Port()),
		daprd.WithConfigManifests(t, fmt.Sprintf(`apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: acl
spec:
  accessControl:
    defaultAction: allow
    trustDomain: public
    policies:
    - appId: %s
      defaultAction: allow
      trustDomain: public
      namespace: %s
      operations:
      - name: /admin
        httpVerb: ["DELETE"]
        action: deny
`, httpverbPeerAppID, security.CurrentNamespace())),
	)

	return []framework.Option{
		framework.WithProcesses(app, h.sentry, h.daprd),
	}
}

func (h *httpverb) Run(t *testing.T, ctx context.Context) {
	h.sentry.WaitUntilRunning(t, ctx)
	h.daprd.WaitUntilRunning(t, ctx)

	sctx, cancel := context.WithCancel(ctx)
	secProv, err := security.New(sctx, security.Options{
		SentryAddress:           h.sentry.Address(),
		ControlPlaneTrustDomain: "localhost",
		ControlPlaneNamespace:   "default",
		TrustAnchors:            h.sentry.CABundle().X509.TrustAnchors,
		AppID:                   httpverbPeerAppID,
		MTLSEnabled:             true,
		Healthz:                 healthz.New(),
	})
	require.NoError(t, err)
	secErr := make(chan error)
	go func() { secErr <- secProv.Run(sctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case runErr := <-secErr:
			require.NoError(t, runErr)
		case <-time.After(5 * time.Second):
			assert.Fail(t, "timed out waiting for security provider to stop")
		}
	})
	sec, err := secProv.Handler(sctx)
	require.NoError(t, err)

	calleeID, err := spiffeid.FromSegments(spiffeid.RequireTrustDomainFromString("public"), "ns", h.daprd.Namespace(), h.daprd.AppID())
	require.NoError(t, err)
	conn, err := grpc.NewClient(h.daprd.InternalGRPCAddress(), sec.GRPCDialOptionMTLS(calleeID))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	client := internalv1pb.NewServiceInvocationClient(conn)

	hits := func(key string) int {
		h.lock.Lock()
		defer h.lock.Unlock()
		return h.hits[key]
	}

	callLocal := func(verb commonv1.HTTPExtension_Verb, md map[string]*internalv1pb.ListStringValue) error {
		_, err := client.CallLocal(ctx, &internalv1pb.InternalInvokeRequest{
			Ver: internalv1pb.APIVersion_V1,
			Message: &commonv1.InvokeRequest{
				Method:        "admin",
				HttpExtension: &commonv1.HTTPExtension{Verb: verb},
			},
			Metadata: md,
		})
		return err
	}

	daprdMetadata := map[string]*internalv1pb.ListStringValue{
		"content-type":       {Values: []string{"application/json"}},
		"dapr-caller-app-id": {Values: []string{httpverbPeerAppID}},
		"dapr-callee-app-id": {Values: []string{h.daprd.AppID()}},
	}

	for name, md := range map[string]map[string]*internalv1pb.ListStringValue{
		"no metadata": nil,
		"metadata":    daprdMetadata,
	} {
		t.Run("DELETE denied with "+name, func(t *testing.T) {
			before := hits("DELETE /admin")
			err := callLocal(commonv1.HTTPExtension_DELETE, md)
			assert.Equal(t, codes.PermissionDenied, status.Code(err), "err: %v", err)
			assert.Equal(t, before, hits("DELETE /admin"), "app received DELETE /admin")
		})

		t.Run("GET allowed with "+name, func(t *testing.T) {
			before := hits("GET /admin")
			require.NoError(t, callLocal(commonv1.HTTPExtension_GET, md))
			assert.Equal(t, before+1, hits("GET /admin"))
		})
	}
}
