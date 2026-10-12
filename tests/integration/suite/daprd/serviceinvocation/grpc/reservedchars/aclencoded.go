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
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
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
	suite.Register(new(aclencoded))
}

// aclencoded verifies that a gRPC method containing a percent-encoded '/',
// '\' or '.' is rejected. The callee ACL treats such a sequence as a literal
// character, but the callee's HTTP app decodes it, so it would see a path the
// ACL never checked.
type aclencoded struct {
	caller    *procdaprd.Daprd
	denyList  *procdaprd.Daprd
	allowList *procdaprd.Daprd
	appHits   atomic.Int64
}

func (a *aclencoded) Setup(t *testing.T) []framework.Option {
	srv := prochttp.New(t, prochttp.WithHandler(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		a.appHits.Add(1)
		fmt.Fprintf(w, "Path=%s RawPath=%s", req.URL.Path, req.URL.RawPath)
	})))

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

	a.caller = procdaprd.New(t)
	callerAppID := a.caller.AppID()

	config := func(name, defaultAction string, ops ...configapi.AppOperationAction) configapi.Configuration {
		return configapi.Configuration{
			TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "Configuration"},
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
			Spec: configapi.ConfigurationSpec{
				NameResolutionSpec: &configapi.NameResolutionSpec{Component: "mdns"},
				MTLSSpec: &configapi.MTLSSpec{
					ControlPlaneTrustDomain: "localhost",
					SentryAddress:           sen.Address(),
				},
				AccessControlSpec: &configapi.AccessControlSpec{
					DefaultAction: defaultAction,
					TrustDomain:   "public",
					AppPolicies: []configapi.AppPolicySpec{
						{
							AppName:             callerAppID,
							DefaultAction:       defaultAction,
							TrustDomain:         "public",
							Namespace:           "default",
							AppOperationActions: ops,
						},
					},
				},
			},
		}
	}

	kubeapi := kubernetes.New(t,
		kubernetes.WithBaseOperatorAPI(t,
			spiffeid.RequireTrustDomainFromString("localhost"),
			"default",
			sen.Port(),
		),
		kubernetes.WithClusterDaprConfigurationList(t, &configapi.ConfigurationList{
			TypeMeta: metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "ConfigurationList"},
			Items: []configapi.Configuration{
				config("deny-list", "allow",
					configapi.AppOperationAction{Operation: "/admin", HTTPVerb: []string{"*"}, Action: "deny"},
					configapi.AppOperationAction{Operation: "/admin/*", HTTPVerb: []string{"*"}, Action: "deny"},
					configapi.AppOperationAction{Operation: "/admin/**", HTTPVerb: []string{"*"}, Action: "deny"},
				),
				config("allow-list", "deny",
					configapi.AppOperationAction{Operation: "/public/*", HTTPVerb: []string{"*"}, Action: "allow"},
				),
			},
		}),
	)

	op := operator.New(t,
		operator.WithNamespace("default"),
		operator.WithKubeconfigPath(kubeapi.KubeconfigPath(t)),
		operator.WithTrustAnchorsFile(sen.TrustAnchorsFile(t)),
	)

	daprdOpts := func(cfg string, extra ...procdaprd.Option) []procdaprd.Option {
		return append([]procdaprd.Option{
			procdaprd.WithConfigs(cfg),
			procdaprd.WithExecOptions(exec.WithEnvVars(t, "DAPR_TRUST_ANCHORS", string(bundle.X509.TrustAnchors))),
			procdaprd.WithSentryAddress(sen.Address()),
			procdaprd.WithEnableMTLS(true),
			procdaprd.WithMode("kubernetes"),
			procdaprd.WithPlacementAddresses(pl.Address()),
			procdaprd.WithSchedulerAddresses(sch.Address()),
			procdaprd.WithControlPlaneAddress(op.Address()),
			procdaprd.WithDisableK8sSecretStore(true),
			procdaprd.WithNamespace("default"),
		}, extra...)
	}

	a.denyList = procdaprd.New(t, daprdOpts("deny-list", procdaprd.WithAppPort(srv.Port()))...)
	a.allowList = procdaprd.New(t, daprdOpts("allow-list", procdaprd.WithAppPort(srv.Port()))...)
	a.caller = procdaprd.New(t, daprdOpts("deny-list", procdaprd.WithAppID(callerAppID))...)

	return []framework.Option{
		framework.WithProcesses(srv, sen, kubeapi, pl, sch, op, a.denyList, a.allowList, a.caller),
	}
}

func (a *aclencoded) Run(t *testing.T, ctx context.Context) {
	a.caller.WaitUntilRunning(t, ctx)
	a.denyList.WaitUntilRunning(t, ctx)
	a.allowList.WaitUntilRunning(t, ctx)

	client := a.caller.GRPCClient(t, ctx)

	invoke := func(t *testing.T, target *procdaprd.Daprd, method string) (string, codes.Code) {
		t.Helper()
		resp, err := client.InvokeService(ctx, &rtv1.InvokeServiceRequest{
			Id: target.AppID(),
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

	t.Run("deny-list: allowed method reaches app", func(t *testing.T) {
		body, code := invoke(t, a.denyList, "public")
		assert.Equal(t, codes.OK, code)
		assert.Equal(t, "Path=/public RawPath=", body)
	})

	t.Run("allow-list: allowed method reaches app", func(t *testing.T) {
		body, code := invoke(t, a.allowList, "public/x")
		assert.Equal(t, codes.OK, code)
		assert.Equal(t, "Path=/public/x RawPath=", body)
	})

	for _, tc := range []struct {
		name   string
		target *procdaprd.Daprd
		method string
	}{
		{name: "deny-list: denied method", target: a.denyList, method: "admin/secret"},
		{name: "allow-list: denied method", target: a.allowList, method: "admin/secret"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := a.appHits.Load()
			body, code := invoke(t, tc.target, tc.method)
			assert.Equal(t, codes.Internal, code)
			assert.Contains(t, body, "access control policy has denied access")
			assert.Equalf(t, before, a.appHits.Load(), "app was reached: %s", body)
		})
	}

	// gRPC methods are not URL-decoded, so %2F, %5C and %2E reach daprd as
	// is. A literal backslash is re-encoded to %5C on the wire to the app.
	// These must be rejected before the callee app is called.
	for _, tc := range []struct {
		name   string
		target *procdaprd.Daprd
		method string
	}{
		{name: "deny-list: encoded slash", target: a.denyList, method: "admin%2Fsecret"},
		{name: "deny-list: encoded lowercase slash", target: a.denyList, method: "admin%2fsecret"},
		{name: "deny-list: encoded backslash", target: a.denyList, method: "admin%5Csecret"},
		{name: "deny-list: literal backslash", target: a.denyList, method: `admin\secret`},
		{name: "deny-list: encoded dot segment", target: a.denyList, method: "%2E%2E/admin"},
		{name: "allow-list: encoded traversal inside wildcard", target: a.allowList, method: "public/x%2F..%2F..%2Fadmin"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := a.appHits.Load()
			body, code := invoke(t, tc.target, tc.method)
			assert.Equalf(t, codes.Internal, code, "response: %s", body)
			assert.Contains(t, body, "InvalidArgument desc = invalid method")
			assert.Equalf(t, before, a.appHits.Load(), "app was reached: %s", body)
		})
	}
}
