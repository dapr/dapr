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
	suite.Register(new(acladmindenygrpc))
}

// acladmindenygrpc is the gRPC-client counterpart of acladmindeny: default
// action allow, explicit deny rules on /admin (exact) and /admin/*
// (wildcard prefix), verifying the ACL evaluates the exact trailing-slash
// preserving normalized method the app receives (dapr/dapr#7686,
// dapr/dapr#9833).
type acladmindenygrpc struct {
	caller    *procdaprd.Daprd
	callee    *procdaprd.Daprd
	adminHits *atomic.Int64
}

func (r *acladmindenygrpc) Setup(t *testing.T) []framework.Option {
	var adminHits atomic.Int64
	r.adminHits = &adminHits

	handler := http.NewServeMux()
	handler.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
		w.Write([]byte("path:" + req.URL.Path))
	})
	handler.HandleFunc("/admin/", func(w http.ResponseWriter, req *http.Request) {
		adminHits.Add(1)
		w.Write([]byte("admin"))
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
		ObjectMeta: metav1.ObjectMeta{Name: "acl-admin-deny-config-grpc", Namespace: "default"},
		Spec: configapi.ConfigurationSpec{
			NameResolutionSpec: &configapi.NameResolutionSpec{Component: "mdns"},
			MTLSSpec: &configapi.MTLSSpec{
				ControlPlaneTrustDomain: "localhost",
				SentryAddress:           sen.Address(),
			},
			AccessControlSpec: &configapi.AccessControlSpec{
				DefaultAction: "allow",
				TrustDomain:   "public",
				AppPolicies: []configapi.AppPolicySpec{
					{
						AppName:       callerAppID,
						DefaultAction: "allow",
						TrustDomain:   "public",
						Namespace:     "default",
						AppOperationActions: []configapi.AppOperationAction{
							{Operation: "/admin", HTTPVerb: []string{"*"}, Action: "deny"},
							{Operation: "/admin/*", HTTPVerb: []string{"*"}, Action: "deny"},
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
			procdaprd.WithConfigs("acl-admin-deny-config-grpc"),
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

	// Callee is an HTTP app so the deny rule is exercised across the
	// same constructRequest URL-building path used by real HTTP apps.
	r.callee = procdaprd.New(t, daprdOpts(procdaprd.WithAppPort(srv.Port()))...)
	r.caller = procdaprd.New(t, daprdOpts(procdaprd.WithAppID(callerAppID))...)

	return []framework.Option{
		framework.WithProcesses(srv, sen, kubeapi, pl, sch, op, r.callee, r.caller),
	}
}

func (r *acladmindenygrpc) Run(t *testing.T, ctx context.Context) {
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

	t.Run("sanity: unrelated path allowed by default action", func(t *testing.T) {
		_, code := invoke(t, "public")
		assert.Equal(t, codes.OK, code)
	})

	t.Run("exact deny: admin with trailing slash is denied", func(t *testing.T) {
		body, code := invoke(t, "admin/")
		assert.Equalf(t, codes.Internal, code, "callee received: %s", body)
	})

	t.Run("wildcard deny: admin subpath with trailing slash is denied", func(t *testing.T) {
		body, code := invoke(t, "admin/secret/")
		assert.Equalf(t, codes.Internal, code, "callee received: %s", body)
	})

	t.Run("traversal into admin with trailing slash still denied", func(t *testing.T) {
		// gRPC methods are a plain string field, not a URL, so "../admin/"
		// is sent to daprd exactly as written (no client-side collapsing).
		// NormalizeMethod strips the leading "../" on a rootless relative
		// path, normalizing this to "admin/", which the exact deny still
		// matches.
		body, code := invoke(t, "../admin/")
		assert.Equalf(t, codes.Internal, code,
			"should normalize to admin/ and be denied: callee received: %s", body)
	})

	t.Run("callee /admin/ handler was never reached", func(t *testing.T) {
		assert.Equalf(t, int64(0), r.adminHits.Load(),
			"the /admin/ handler was hit %d times — ACL bypass detected!", r.adminHits.Load())
	})
}
