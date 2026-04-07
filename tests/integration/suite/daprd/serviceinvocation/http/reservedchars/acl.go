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
	suite.Register(new(acl))
}

type acl struct {
	caller *procdaprd.Daprd
	callee *procdaprd.Daprd
}

func (r *acl) Setup(t *testing.T) []framework.Option {
	handler := http.NewServeMux()
	handler.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
		w.Write([]byte(req.URL.Path))
	})
	handler.HandleFunc("/unauthorized", func(w http.ResponseWriter, req *http.Request) {
		w.Write([]byte("unauthorized"))
	})
	srv := prochttp.New(t, prochttp.WithHandler(handler))

	sen := sentry.New(t)
	bundle := sen.CABundle()
	taFile := filepath.Join(t.TempDir(), "ca.pem")
	require.NoError(t, os.WriteFile(taFile, bundle.TrustAnchors, 0o600))

	pl := placement.New(t,
		placement.WithEnableTLS(true),
		placement.WithTrustAnchorsFile(taFile),
		placement.WithSentryAddress(sen.Address()),
	)
	sch := scheduler.New(t,
		scheduler.WithSentry(sen),
		scheduler.WithID("dapr-scheduler-server-0"),
	)

	// Create caller first so we know its appID for the ACL policy.
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

	daprdOpts := func(appPort ...int) []procdaprd.Option {
		opts := []procdaprd.Option{
			procdaprd.WithConfigs("acl-config"),
			procdaprd.WithExecOptions(exec.WithEnvVars(t, "DAPR_TRUST_ANCHORS", string(bundle.TrustAnchors))),
			procdaprd.WithSentryAddress(sen.Address()),
			procdaprd.WithEnableMTLS(true),
			procdaprd.WithMode("kubernetes"),
			procdaprd.WithPlacementAddresses(pl.Address()),
			procdaprd.WithSchedulerAddresses(sch.Address()),
			procdaprd.WithControlPlaneAddress(op.Address()),
			procdaprd.WithDisableK8sSecretStore(true),
			procdaprd.WithNamespace("default"),
		}
		if len(appPort) > 0 {
			opts = append(opts, procdaprd.WithAppPort(appPort[0]))
		}
		return opts
	}

	r.callee = procdaprd.New(t, daprdOpts(srv.Port())...)
	r.caller = procdaprd.New(t, append(daprdOpts(), procdaprd.WithAppID(callerAppID))...)

	return []framework.Option{
		framework.WithProcesses(srv, sen, kubeapi, pl, sch, op, r.callee, r.caller),
	}
}

func (r *acl) Run(t *testing.T, ctx context.Context) {
	r.caller.WaitUntilRunning(t, ctx)
	r.callee.WaitUntilRunning(t, ctx)

	httpClient := client.HTTP(t)

	invoke := func(t *testing.T, methodSuffix string) (int, string, string) {
		t.Helper()
		reqURL := fmt.Sprintf(
			"http://localhost:%d/v1.0/invoke/%s/method/%s",
			r.caller.HTTPPort(),
			r.callee.AppID(),
			methodSuffix,
		)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
		require.NoError(t, err)
		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		return resp.StatusCode, string(body), reqURL
	}

	t.Run("allowed path succeeds", func(t *testing.T) {
		status, body, sent := invoke(t, "public")
		assert.Equalf(t, http.StatusOK, status, "caller sent: %s, callee received: %s", sent, body)
		assert.Equal(t, "/public", body)
	})

	t.Run("denied path is rejected", func(t *testing.T) {
		status, body, sent := invoke(t, "admin")
		assert.Equalf(t, http.StatusForbidden, status, "caller sent: %s, callee received: %s", sent, body)
	})

	// NormalizeMethod rejects methods containing decoded #, ?, or % with
	// HTTP 500 before ACL evaluation.

	t.Run("hash cannot truncate path to bypass ACL", func(t *testing.T) {
		// Go's HTTP client decodes %23 → '#' and resolves '../..' in the
		// URL path before the request reaches the handler. The handler
		// sees the resolved path ('admin'), not the raw attack payload.
		// ACL correctly denies 'admin'.
		status, body, sent := invoke(t, "public%23../../admin")
		assert.Equalf(t, http.StatusForbidden, status, "caller sent: %s, callee received: %s", sent, body)
	})

	t.Run("question mark cannot inject query to bypass ACL", func(t *testing.T) {
		// Decoded method contains '?' → rejected by NormalizeMethod.
		status, body, sent := invoke(t, "admin%3Fignored")
		assert.Equalf(t, http.StatusInternalServerError, status, "caller sent: %s, callee received: %s", sent, body)
	})

	t.Run("percent in path does not bypass ACL", func(t *testing.T) {
		// %25 decodes to literal '%' which is allowed per RFC 3986.
		// The decoded path admin%stream is denied by ACL.
		status, body, sent := invoke(t, "admin%25stream")
		assert.Equalf(t, http.StatusForbidden, status, "caller sent: %s, callee received: %s", sent, body)
	})

	t.Run("quote in path does not bypass ACL", func(t *testing.T) {
		status, body, sent := invoke(t, "admin%22stream")
		assert.Equalf(t, http.StatusForbidden, status, "caller sent: %s, callee received: %s", sent, body)
	})

	t.Run("asterisk in path does not match as wildcard", func(t *testing.T) {
		status, body, sent := invoke(t, "admin%2Astream")
		assert.Equalf(t, http.StatusForbidden, status, "caller sent: %s, callee received: %s", sent, body)
	})

	t.Run("backslash in path does not bypass ACL", func(t *testing.T) {
		status, body, sent := invoke(t, "admin%5Cstream")
		assert.Equalf(t, http.StatusForbidden, status, "caller sent: %s, callee received: %s", sent, body)
	})

	// Path traversal is resolved before ACL check. Encoded slashes decode
	// to real slashes, then ../ segments are resolved, and ACL sees the
	// clean path. admin/../public resolves to /public which is allowed.

	t.Run("encoded slash traversal resolves to allowed path", func(t *testing.T) {
		// admin%2F..%2Fpublic decodes to admin/../public → resolves to /public → allowed.
		status, body, sent := invoke(t, "admin%2F..%2Fpublic")
		assert.Equalf(t, http.StatusOK, status, "caller sent: %s, callee received: %s", sent, body)
		assert.Equal(t, "/public", body)
	})

	t.Run("double encoded traversal resolves to allowed path", func(t *testing.T) {
		// admin%2F..%2F..%2Fpublic decodes to admin/../../public → resolves to /public → allowed.
		status, body, sent := invoke(t, "admin%2F..%2F..%2Fpublic")
		assert.Equalf(t, http.StatusOK, status, "caller sent: %s, callee received: %s", sent, body)
		assert.Equal(t, "/public", body)
	})

	t.Run("encoded traversal from nested denied path resolves to allowed", func(t *testing.T) {
		// admin%2Fsecret%2F..%2F..%2Fpublic decodes to admin/secret/../../public → resolves to /public → allowed.
		status, body, sent := invoke(t, "admin%2Fsecret%2F..%2F..%2Fpublic")
		assert.Equalf(t, http.StatusOK, status, "caller sent: %s, callee received: %s", sent, body)
		assert.Equal(t, "/public", body)
	})

	t.Run("encoded backslash traversal", func(t *testing.T) {
		status, body, sent := invoke(t, "admin%5C..%5Cpublic")
		assert.Equalf(t, http.StatusForbidden, status, "caller sent: %s, callee received: %s", sent, body)
	})

	t.Run("mixed slash and backslash traversal", func(t *testing.T) {
		status, body, sent := invoke(t, "admin%2F..%5C..%2Fpublic")
		assert.Equalf(t, http.StatusForbidden, status, "caller sent: %s, callee received: %s", sent, body)
	})

	t.Run("dot segment only traversal", func(t *testing.T) {
		// ..%2Fpublic decodes to ../public → resolves to /public → allowed.
		status, body, sent := invoke(t, "..%2Fpublic")
		assert.Equalf(t, http.StatusOK, status, "caller sent: %s, callee received: %s", sent, body)
		assert.Equal(t, "/public", body)
	})

	t.Run("encoded null byte in path", func(t *testing.T) {
		// %00 decodes to \x00 → rejected by NormalizeMethod.
		status, body, sent := invoke(t, "admin%00public")
		assert.Equalf(t, http.StatusInternalServerError, status, "caller sent: %s, callee received: %s", sent, body)
	})

	t.Run("multiple reserved characters combined", func(t *testing.T) {
		// %23%3F%25 decodes to #?% → rejected by NormalizeMethod.
		status, body, sent := invoke(t, "admin%23%3F%25bypass")
		assert.Equalf(t, http.StatusInternalServerError, status, "caller sent: %s, callee received: %s", sent, body)
	})

	// Go's HTTP stack decodes %23 → '#' and %3F → '?' which have URL
	// semantics (fragment delimiter and query separator). Combined with
	// '../' path traversal, the HTTP framework resolves the path before
	// the handler. The handler sees the resolved path, and the ACL
	// correctly denies it. The attack is blocked at 403, not 500.

	t.Run("hash hides traversal after allowed prefix", func(t *testing.T) {
		// HTTP client resolves decoded '#' and '../' → handler sees 'admin' → ACL denies.
		status, body, sent := invoke(t, "public%23%2F..%2Fadmin")
		assert.Equalf(t, http.StatusForbidden, status, "caller sent: %s, callee received: %s", sent, body)
	})

	t.Run("question mark hides traversal after allowed prefix", func(t *testing.T) {
		// HTTP framework resolves decoded '?' and '../' → handler sees
		// resolved path → ACL denies.
		status, body, sent := invoke(t, "public%3F%2F..%2Fadmin")
		assert.Equalf(t, http.StatusForbidden, status, "caller sent: %s, callee received: %s", sent, body)
	})

	t.Run("hash hides deep traversal to reach denied path", func(t *testing.T) {
		// HTTP framework resolves '../' → handler sees denied path → ACL denies.
		status, body, sent := invoke(t, "public%23%2F..%2F..%2Fsecret%2Fadmin")
		assert.Equalf(t, http.StatusForbidden, status, "caller sent: %s, callee received: %s", sent, body)
	})

	t.Run("allowed prefix with hash then null byte and traversal", func(t *testing.T) {
		// HTTP framework resolves traversal → handler sees resolved path → ACL denies.
		status, body, sent := invoke(t, "public%23%00%2F..%2Fadmin")
		assert.Equalf(t, http.StatusForbidden, status, "caller sent: %s, callee received: %s", sent, body)
	})

	t.Run("percent with traversal after allowed prefix", func(t *testing.T) {
		// %25 decodes to literal '%' (allowed per RFC 3986).
		// Full path: public%/../admin → path.Clean resolves to admin → denied.
		status, body, sent := invoke(t, "public%25%2F..%2Fadmin")
		assert.Equalf(t, http.StatusForbidden, status, "caller sent: %s, callee received: %s", sent, body)
	})

	// Traversal with only encoded slashes (no #/?/%/\x00): resolves before
	// ACL. public/../unauthorized resolves to /unauthorized which is denied by ACL.
	t.Run("traversal reaches unrelated handler behind ACL", func(t *testing.T) {
		status, body, sent := invoke(t, "public%2F..%2Funauthorized")
		assert.Equalf(t, http.StatusForbidden, status, "caller sent: %s, callee received: %s", sent, body)
	})

	// Literal (unencoded) characters — these test what a standard HTTP
	// client sends when the raw character appears in the URL. The HTTP
	// client interprets '#' as fragment (stripped) and '?' as query
	// (splits path). These are the realistic attack vectors.

	t.Run("literal hash strips path to allowed prefix", func(t *testing.T) {
		// HTTP client strips '#../../admin' as fragment, sends just /public.
		// The ACL correctly sees /public and allows it — this is NOT a
		// bypass because the attack payload was never sent.
		status, body, sent := invoke(t, "public#../../admin")
		assert.Equalf(t, http.StatusOK, status, "caller sent: %s, callee received: %s", sent, body)
		assert.Equal(t, "/public", body)
	})

	t.Run("literal question mark on denied path", func(t *testing.T) {
		// HTTP client sends /admin with query 'bypass' — ACL evaluates /admin.
		status, body, sent := invoke(t, "admin?bypass")
		assert.Equalf(t, http.StatusForbidden, status, "caller sent: %s, callee received: %s", sent, body)
	})

	t.Run("literal quote on denied path", func(t *testing.T) {
		status, body, sent := invoke(t, `admin"stream`)
		assert.Equalf(t, http.StatusForbidden, status, "caller sent: %s, callee received: %s", sent, body)
	})

	t.Run("literal asterisk on denied path", func(t *testing.T) {
		status, body, sent := invoke(t, "admin*stream")
		assert.Equalf(t, http.StatusForbidden, status, "caller sent: %s, callee received: %s", sent, body)
	})

	t.Run("literal backslash on denied path", func(t *testing.T) {
		status, body, sent := invoke(t, `admin\stream`)
		assert.Equalf(t, http.StatusForbidden, status, "caller sent: %s, callee received: %s", sent, body)
	})

	// Verify callee receives the normalized method, not the raw traversal.
	t.Run("callee receives clean path from encoded traversal", func(t *testing.T) {
		status, body, sent := invoke(t, "admin%2F..%2Fpublic")
		assert.Equalf(t, http.StatusOK, status, "caller sent: %s, callee received: %s", sent, body)
		assert.Equal(t, "/public", body)
	})

	// Duplicate slashes are collapsed by path.Clean. /public/extra
	// matches the /public prefix in the ACL trie, so it is allowed.
	t.Run("duplicate slashes collapsed", func(t *testing.T) {
		status, body, sent := invoke(t, "public///extra")
		assert.Equalf(t, http.StatusOK, status, "caller sent: %s, callee received: %s", sent, body)
		assert.Equal(t, "/public/extra", body)
	})

	// Traversal that exits and re-enters the allowed path prefix.
	t.Run("traversal exit and reenter allowed path", func(t *testing.T) {
		// public/../public → resolves to /public → allowed.
		status, body, sent := invoke(t, "public%2F..%2Fpublic")
		assert.Equalf(t, http.StatusOK, status, "caller sent: %s, callee received: %s", sent, body)
		assert.Equal(t, "/public", body)
	})

	// Double traversal from deeply nested path.
	t.Run("deep nested traversal to allowed", func(t *testing.T) {
		status, body, sent := invoke(t, "a%2Fb%2Fc%2F..%2F..%2F..%2Fpublic")
		assert.Equalf(t, http.StatusOK, status, "caller sent: %s, callee received: %s", sent, body)
		assert.Equal(t, "/public", body)
	})
}
