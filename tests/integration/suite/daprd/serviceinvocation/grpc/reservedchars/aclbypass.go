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

package reservedchars

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	configapi "github.com/dapr/dapr/pkg/apis/configuration/v1alpha1"
	commonv1 "github.com/dapr/dapr/pkg/proto/common/v1"
	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	procdaprd "github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/kubernetes"
	"github.com/dapr/dapr/tests/integration/framework/process/operator"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(aclbypass))
}

type aclbypass struct {
	caller           *procdaprd.Daprd
	callee           *procdaprd.Daprd
	unauthorizedHits *atomic.Int64
}

func (r *aclbypass) Setup(t *testing.T) []framework.Option {
	var unauthorizedHits atomic.Int64
	r.unauthorizedHits = &unauthorizedHits

	handler := http.NewServeMux()
	handler.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
		w.Write([]byte("path:" + req.URL.Path))
	})
	handler.HandleFunc("/unauthorized", func(w http.ResponseWriter, req *http.Request) {
		unauthorizedHits.Add(1)
		w.Write([]byte("unauthorized"))
	})
	srv := prochttp.New(t, prochttp.WithHandler(handler))

	sen := sentry.New(t)
	bundle := sen.CABundle()
	taFile := filepath.Join(t.TempDir(), "ca.pem")
	require.NoError(t, os.WriteFile(taFile, bundle.X509.TrustAnchors, 0o600))

	pl := placement.New(t,
		placement.WithEnableTLS(true),
		placement.WithTrustAnchorsFile(taFile),
		placement.WithSentryAddress(sen.Address()),
	)
	sch := scheduler.New(t,
		scheduler.WithSentry(sen),
		scheduler.WithID("dapr-scheduler-server-0"),
	)

	r.caller = procdaprd.New(t)
	callerAppID := r.caller.AppID()

	cnf := configapi.Configuration{
		TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "Configuration"},
		ObjectMeta: metav1.ObjectMeta{Name: "acl-config", Namespace: "default"},
		Spec: configapi.ConfigurationSpec{
			NameResolutionSpec: &configapi.NameResolutionSpec{Component: "mdns"},
			MTLSSpec: &configapi.MTLSSpec{
				ControlPlaneTrustDomain: "localhost",
				SentryAddress:           sen.Address(),
			},
			AccessControlSpec: &configapi.AccessControlSpec{
				DefaultAction: "deny",
				TrustDomain:   "public",
				AppPolicies: []configapi.AppPolicySpec{
					{
						AppName:       callerAppID,
						DefaultAction: "deny",
						TrustDomain:   "public",
						Namespace:     "default",
						AppOperationActions: []configapi.AppOperationAction{
							{Operation: "/public", HTTPVerb: []string{"*"}, Action: "allow"},
						},
					},
				},
			},
		},
	}

	kubeapi := kubernetes.New(t,
		kubernetes.WithBaseOperatorAPI(t,
			spiffeid.RequireTrustDomainFromString("localhost"),
			"default",
			sen.Port(),
		),
		kubernetes.WithClusterDaprConfigurationList(t, &configapi.ConfigurationList{
			TypeMeta: metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "ConfigurationList"},
			Items:    []configapi.Configuration{cnf},
		}),
	)

	op := operator.New(t,
		operator.WithNamespace("default"),
		operator.WithKubeconfigPath(kubeapi.KubeconfigPath(t)),
		operator.WithTrustAnchorsFile(sen.TrustAnchorsFile(t)),
	)

	daprdOpts := func(extra ...procdaprd.Option) []procdaprd.Option {
		opts := []procdaprd.Option{
			procdaprd.WithConfigs("acl-config"),
			procdaprd.WithExecOptions(exec.WithEnvVars(t, "DAPR_TRUST_ANCHORS", string(bundle.X509.TrustAnchors))),
			procdaprd.WithSentryAddress(sen.Address()),
			procdaprd.WithEnableMTLS(true),
			procdaprd.WithMode("kubernetes"),
			procdaprd.WithPlacementAddresses(pl.Address()),
			procdaprd.WithSchedulerAddresses(sch.Address()),
			procdaprd.WithControlPlaneAddress(op.Address()),
			procdaprd.WithDisableK8sSecretStore(true),
			procdaprd.WithNamespace("default"),
		}
		return append(opts, extra...)
	}

	// Callee is an HTTP app — the method goes through constructRequest/URL parsing
	r.callee = procdaprd.New(t, daprdOpts(procdaprd.WithAppPort(srv.Port()))...)
	r.caller = procdaprd.New(t, daprdOpts(procdaprd.WithAppID(callerAppID))...)

	return []framework.Option{
		framework.WithProcesses(srv, sen, kubeapi, pl, sch, op, r.callee, r.caller),
	}
}

func (r *aclbypass) Run(t *testing.T, ctx context.Context) {
	r.caller.WaitUntilRunning(t, ctx)
	r.callee.WaitUntilRunning(t, ctx)

	//nolint:staticcheck
	conn, err := grpc.DialContext(ctx, r.caller.GRPCAddress(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	client := rtv1.NewDaprClient(conn)

	invoke := func(t *testing.T, method string) (string, codes.Code) {
		t.Helper()
		resp, err := client.InvokeService(ctx, &rtv1.InvokeServiceRequest{
			Id: r.callee.AppID(),
			Message: &commonv1.InvokeRequest{
				Method:        method,
				HttpExtension: &commonv1.HTTPExtension{Verb: commonv1.HTTPExtension_GET},
			},
		})
		if err != nil {
			return status.Convert(err).Message(), status.Convert(err).Code()
		}
		return string(resp.GetData().GetValue()), codes.OK
	}

	t.Run("sanity: allowed path works", func(t *testing.T) {
		body, code := invoke(t, "/public")
		assert.Equal(t, codes.OK, code)
		assert.Contains(t, body, "public")
	})

	t.Run("sanity: denied path rejected", func(t *testing.T) {
		_, code := invoke(t, "/unauthorized")
		assert.Equal(t, codes.Internal, code)
	})

	// NormalizeMethod no longer decodes percent-encoding. Via gRPC, %23 and
	// %3F are literal characters (not # or ?). They pass NormalizeMethod.
	// path.Clean resolves ../ and the result reaches ACL evaluation.

	t.Run("encoded hash with traversal reaches unauthorized", func(t *testing.T) {
		// /public%23/../unauthorized → path.Clean → /unauthorized → ACL denies
		body, code := invoke(t, "/public%23/../unauthorized")
		assert.Equalf(t, codes.Internal, code,
			"should resolve to /unauthorized and be denied by ACL: callee received: %s", body)
	})

	t.Run("encoded question mark with traversal", func(t *testing.T) {
		// /public%3F/../unauthorized → path.Clean → /unauthorized → ACL denies
		body, code := invoke(t, "/public%3F/../unauthorized")
		assert.Equalf(t, codes.Internal, code,
			"should resolve to /unauthorized and be denied by ACL: callee received: %s", body)
	})

	t.Run("double dot with encoded hash", func(t *testing.T) {
		// /public%23/../../unauthorized → path.Clean → /unauthorized → ACL denies
		body, code := invoke(t, "/public%23/../../unauthorized")
		assert.Equalf(t, codes.Internal, code,
			"should resolve to /unauthorized and be denied by ACL: callee received: %s", body)
	})

	t.Run("nested path with encoded hash traversal", func(t *testing.T) {
		// /public/sub%23/../../unauthorized → path.Clean → /unauthorized → ACL denies
		body, code := invoke(t, "/public/sub%23/../../unauthorized")
		assert.Equalf(t, codes.Internal, code,
			"should resolve to /unauthorized and be denied by ACL: callee received: %s", body)
	})

	t.Run("carriage return with traversal", func(t *testing.T) {
		// /public\r/../unauthorized → NormalizeMethod rejects \r (control char)
		body, code := invoke(t, "/public\r/../unauthorized")
		assert.NotEqualf(t, codes.OK, code,
			"should be rejected — \\r is a control character: callee received: %s", body)
	})

	// Plain traversal (no forbidden chars) is resolved by path.Clean.
	// /public/../unauthorized resolves to /unauthorized which is denied by ACL.
	t.Run("plain traversal to unauthorized is denied by ACL", func(t *testing.T) {
		body, code := invoke(t, "/public/../unauthorized")
		assert.Equalf(t, codes.Internal, code,
			"should resolve to /unauthorized and be denied by ACL: callee received: %s", body)
	})

	// Percent-encoded slash traversal via gRPC — the key attack vector.
	// NormalizeMethod does NOT decode %2F for gRPC methods, so the ACL
	// must also NOT decode them. Both sides must see the same literal string.

	t.Run("encoded slash traversal to allowed path is denied", func(t *testing.T) {
		// admin%2F..%2Fpublic is an opaque string with no real slashes.
		// ACL must NOT decode %2F and treat it as traversal to /public.
		body, code := invoke(t, "admin%2F..%2Fpublic")
		assert.Equalf(t, codes.Internal, code,
			"should NOT be allowed — %%2F must not be decoded by ACL: callee received: %s", body)
	})

	t.Run("double encoded slash traversal is denied", func(t *testing.T) {
		body, code := invoke(t, "admin%2F..%2F..%2Fpublic")
		assert.Equalf(t, codes.Internal, code,
			"should NOT be allowed — %%2F must not be decoded by ACL: callee received: %s", body)
	})

	t.Run("encoded slash from nested path is denied", func(t *testing.T) {
		body, code := invoke(t, "admin%2Fsecret%2F..%2F..%2Fpublic")
		assert.Equalf(t, codes.Internal, code,
			"should NOT be allowed — %%2F must not be decoded by ACL: callee received: %s", body)
	})

	// Prove the callee's /unauthorized handler was NEVER reached by any
	// of the traversal attacks above. This is the core CVE invariant:
	// if the ACL denies the request, the callee must not be invoked.
	t.Run("callee /unauthorized handler was never reached", func(t *testing.T) {
		assert.Equalf(t, int64(0), r.unauthorizedHits.Load(),
			"the /unauthorized handler was hit %d times — ACL bypass detected!", r.unauthorizedHits.Load())
	})
}
