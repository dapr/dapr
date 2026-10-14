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
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	configapi "github.com/dapr/dapr/pkg/apis/configuration/v1alpha1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
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

// aclencoded verifies that a method which still contains a percent-encoded
// '/', '\' or '.' after the caller daprd decodes the URL once is rejected.
// The callee ACL treats such a sequence as a literal character, but the
// callee's HTTP app decodes it, so it would see a path the ACL never checked.
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

	httpClient := client.HTTP(t)

	invoke := func(t *testing.T, target *procdaprd.Daprd, methodSuffix string) (int, string) {
		t.Helper()
		reqURL := fmt.Sprintf(
			"http://localhost:%d/v1.0/invoke/%s/method/%s",
			a.caller.HTTPPort(),
			target.AppID(),
			methodSuffix,
		)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
		require.NoError(t, err)
		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		return resp.StatusCode, string(body)
	}

	t.Run("deny-list: allowed method reaches app", func(t *testing.T) {
		status, body := invoke(t, a.denyList, "public")
		assert.Equal(t, http.StatusOK, status)
		assert.Equal(t, "Path=/public RawPath=", body)
	})

	t.Run("allow-list: allowed method reaches app", func(t *testing.T) {
		status, body := invoke(t, a.allowList, "public/x")
		assert.Equal(t, http.StatusOK, status)
		assert.Equal(t, "Path=/public/x RawPath=", body)
	})

	for _, tc := range []struct {
		name   string
		target *procdaprd.Daprd
		method string
	}{
		{name: "deny-list: denied method", target: a.denyList, method: "admin/secret"},
		{name: "deny-list: single-encoded slash", target: a.denyList, method: "admin%2Fsecret"},
		{name: "allow-list: denied method", target: a.allowList, method: "admin/secret"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := a.appHits.Load()
			status, body := invoke(t, tc.target, tc.method)
			assert.Equal(t, http.StatusForbidden, status)
			assert.Contains(t, body, "access control policy has denied access")
			assert.Equalf(t, before, a.appHits.Load(), "app was reached: %s", body)
		})
	}

	// The caller daprd decodes the URL once, so the method still contains
	// %2F, %5C or %2E, or a single-encoded %5C becomes a literal backslash
	// that is re-encoded to %5C on the wire to the app. These must be
	// rejected before the callee app is called.
	for _, tc := range []struct {
		name   string
		target *procdaprd.Daprd
		method string
	}{
		{name: "deny-list: double-encoded slash", target: a.denyList, method: "admin%252Fsecret"},
		{name: "deny-list: double-encoded lowercase slash", target: a.denyList, method: "admin%252fsecret"},
		{name: "deny-list: double-encoded backslash", target: a.denyList, method: "admin%255Csecret"},
		{name: "deny-list: single-encoded backslash", target: a.denyList, method: "admin%5Csecret"},
		{name: "deny-list: double-encoded dot segment", target: a.denyList, method: "%252E%252E/admin"},
		{name: "allow-list: double-encoded traversal inside wildcard", target: a.allowList, method: "public/x%252F..%252F..%252Fadmin"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := a.appHits.Load()
			status, body := invoke(t, tc.target, tc.method)
			assert.Equalf(t, http.StatusInternalServerError, status, "response: %s", body)
			assert.Contains(t, body, "InvalidArgument desc = invalid method")
			assert.Equalf(t, before, a.appHits.Load(), "app was reached: %s", body)
		})
	}
}
