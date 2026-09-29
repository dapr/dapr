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
	suite.Register(new(acladmindeny))
}

// acladmindeny covers the default-allow policy shape cicoyle asked for
// on dapr/dapr#9833: default action allow, with explicit deny rules on
// /admin (exact) and /admin/* (wildcard prefix), verifying the ACL
// evaluates the exact trailing-slash-preserving normalized method the
// app would receive (dapr/dapr#7686).
type acladmindeny struct {
	caller     *procdaprd.Daprd
	callee     *procdaprd.Daprd
	adminHits  *atomic.Int64
	publicHits *atomic.Int64
}

func (r *acladmindeny) Setup(t *testing.T) []framework.Option {
	var adminHits atomic.Int64
	var publicHits atomic.Int64
	r.adminHits = &adminHits
	r.publicHits = &publicHits

	onInvoke := func(ctx context.Context, in *commonv1.InvokeRequest) (*commonv1.InvokeResponse, error) {
		method := in.GetMethod()
		if len(method) >= 5 && method[:5] == "admin" {
			adminHits.Add(1)
		} else {
			publicHits.Add(1)
		}
		return &commonv1.InvokeResponse{
			Data: &anypb.Any{Value: []byte(method)},
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
		ObjectMeta: metav1.ObjectMeta{Name: "acl-admin-deny-config", Namespace: "default"},
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
			procdaprd.WithConfigs("acl-admin-deny-config"),
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

func (r *acladmindeny) Run(t *testing.T, ctx context.Context) {
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

	t.Run("sanity: unrelated path allowed by default action", func(t *testing.T) {
		sent, body, status := invoke(t, "public")
		assert.Equalf(t, http.StatusOK, status, "caller sent: %s, callee received: %s", sent, body)
	})

	t.Run("exact deny: admin with trailing slash is denied", func(t *testing.T) {
		sent, body, status := invoke(t, "admin/")
		assert.Equalf(t, http.StatusForbidden, status, "caller sent: %s, callee received: %s", sent, body)
	})

	t.Run("wildcard deny: admin subpath with trailing slash is denied", func(t *testing.T) {
		sent, body, status := invoke(t, "admin/secret/")
		assert.Equalf(t, http.StatusForbidden, status, "caller sent: %s, callee received: %s", sent, body)
	})

	t.Run("traversal into admin with trailing slash still denied", func(t *testing.T) {
		// "../admin/" normalizes to "admin/" (leading "../" on a rootless
		// path is stripped by NormalizeMethod), so the exact /admin deny
		// must still apply.
		sent, body, status := invoke(t, "..%2Fadmin/")
		assert.Equalf(t, http.StatusForbidden, status,
			"caller sent: %s, callee received: %s — should normalize to admin/ and be denied", sent, body)
	})

	t.Run("callee never invoked with an admin method", func(t *testing.T) {
		assert.Equalf(t, int64(0), r.adminHits.Load(),
			"callee received an admin-prefixed method %d times — ACL bypass detected!", r.adminHits.Load())
	})
}
