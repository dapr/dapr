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
	"strings"
	"sync/atomic"
	"testing"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"google.golang.org/protobuf/types/known/anypb"

	configapi "github.com/dapr/dapr/pkg/apis/configuration/v1alpha1"
	commonv1 "github.com/dapr/dapr/pkg/proto/common/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	procdaprd "github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	grpcapp "github.com/dapr/dapr/tests/integration/framework/process/grpc/app"
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

	onInvoke := func(ctx context.Context, in *commonv1.InvokeRequest) (*commonv1.InvokeResponse, error) {
		if strings.Contains(in.GetMethod(), "unauthorized") {
			unauthorizedHits.Add(1)
		}
		return &commonv1.InvokeResponse{
			Data: &anypb.Any{Value: []byte(in.GetMethod())},
		}, nil
	}
	srv := grpcapp.New(t, grpcapp.WithOnInvokeFn(onInvoke))

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

	r.callee = procdaprd.New(t, daprdOpts(
		procdaprd.WithAppProtocol("grpc"),
		procdaprd.WithAppPort(srv.Port(t)),
	)...)
	r.caller = procdaprd.New(t, daprdOpts(procdaprd.WithAppID(callerAppID))...)

	return []framework.Option{
		framework.WithProcesses(srv, sen, kubeapi, pl, sch, op, r.callee, r.caller),
	}
}

func (r *aclbypass) Run(t *testing.T, ctx context.Context) {
	r.caller.WaitUntilRunning(t, ctx)
	r.callee.WaitUntilRunning(t, ctx)

	httpClient := client.HTTP(t)

	invoke := func(t *testing.T, methodSuffix string) (sent string, body string, status int) {
		t.Helper()
		sent = fmt.Sprintf(
			"http://localhost:%d/v1.0/invoke/%s/method/%s",
			r.caller.HTTPPort(),
			r.callee.AppID(),
			methodSuffix,
		)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, sent, nil)
		require.NoError(t, err)
		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		b, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		return sent, string(b), resp.StatusCode
	}

	t.Run("sanity: allowed path works", func(t *testing.T) {
		sent, body, status := invoke(t, "public")
		assert.Equalf(t, http.StatusOK, status, "caller sent: %s, callee received: %s", sent, body)
		assert.Contains(t, body, "public")
	})

	t.Run("sanity: denied path rejected", func(t *testing.T) {
		sent, body, status := invoke(t, "unauthorized")
		assert.Equalf(t, http.StatusForbidden, status, "caller sent: %s, callee received: %s", sent, body)
	})

	// Go's HTTP stack decodes %23 → '#' and %3F → '?' which have URL
	// semantics. Combined with '../' path traversal, the HTTP framework
	// resolves the path before the handler sees it. The handler receives
	// the resolved path and the ACL correctly denies it (403).

	t.Run("encoded hash with traversal", func(t *testing.T) {
		sent, body, status := invoke(t, "public%23/../unauthorized")
		assert.Equalf(t, http.StatusForbidden, status,
			"caller sent: %s, callee received: %s — HTTP framework resolves traversal, ACL denies", sent, body)
	})

	t.Run("encoded question mark with traversal", func(t *testing.T) {
		sent, body, status := invoke(t, "public%3F/../unauthorized")
		assert.Equalf(t, http.StatusForbidden, status,
			"caller sent: %s, callee received: %s — HTTP framework resolves traversal, ACL denies", sent, body)
	})

	t.Run("double dot with encoded hash", func(t *testing.T) {
		sent, body, status := invoke(t, "public%23/../../unauthorized")
		assert.Equalf(t, http.StatusForbidden, status,
			"caller sent: %s, callee received: %s — HTTP framework resolves traversal, ACL denies", sent, body)
	})

	t.Run("nested path with encoded hash traversal", func(t *testing.T) {
		sent, body, status := invoke(t, "public/sub%23/../../unauthorized")
		assert.Equalf(t, http.StatusForbidden, status,
			"caller sent: %s, callee received: %s — HTTP framework resolves traversal, ACL denies", sent, body)
	})

	t.Run("encoded slash traversal to unauthorized", func(t *testing.T) {
		sent, body, status := invoke(t, "public%2F..%2Funauthorized")
		assert.Equalf(t, http.StatusForbidden, status,
			"caller sent: %s, callee received: %s", sent, body)
	})

	t.Run("encoded carriage return with traversal", func(t *testing.T) {
		// HTTP client decodes %0D → \r and resolves /../ traversal before
		// the request reaches Dapr. The handler sees the resolved path
		// ("unauthorized") and the ACL correctly denies it (403).
		sent, body, status := invoke(t, "public%0D/../unauthorized")
		assert.Equalf(t, http.StatusForbidden, status,
			"caller sent: %s, callee received: %s — HTTP framework resolves traversal, ACL denies", sent, body)
	})

	// HTTP decodes %25 → %, NormalizeMethod allows % (not forbidden).
	// path.Clean("public%/../unauthorized") → "unauthorized" → ACL denies → Forbidden.
	t.Run("percent hides traversal", func(t *testing.T) {
		sent, body, status := invoke(t, "public%25/../unauthorized")
		assert.Equalf(t, http.StatusForbidden, status,
			"caller sent: %s, callee received: %s — should resolve to /unauthorized and be denied by ACL", sent, body)
	})

	// Prove the callee was NEVER invoked with an "unauthorized" method
	// by any of the traversal attacks above. This is the core CVE
	// invariant: if the ACL denies the request, the callee must not
	// be invoked with the denied path.
	t.Run("callee never received unauthorized method", func(t *testing.T) {
		assert.Equalf(t, int64(0), r.unauthorizedHits.Load(),
			"callee received 'unauthorized' method %d times — ACL bypass detected!", r.unauthorizedHits.Load())
	})
}
