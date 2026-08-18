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
	"os"
	"path/filepath"
	"testing"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"google.golang.org/protobuf/types/known/anypb"

	configapi "github.com/dapr/dapr/pkg/apis/configuration/v1alpha1"
	commonv1 "github.com/dapr/dapr/pkg/proto/common/v1"
	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
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
	suite.Register(new(acl))
}

type acl struct {
	caller *procdaprd.Daprd
	callee *procdaprd.Daprd
}

func (r *acl) Setup(t *testing.T) []framework.Option {
	onInvoke := func(ctx context.Context, in *commonv1.InvokeRequest) (*commonv1.InvokeResponse, error) {
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
							{Operation: "/public", Action: "allow"},
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
	r.caller = procdaprd.New(t, daprdOpts(
		procdaprd.WithAppID(callerAppID),
	)...)

	return []framework.Option{
		framework.WithProcesses(srv, sen, kubeapi, pl, sch, op, r.callee, r.caller),
	}
}

func (r *acl) Run(t *testing.T, ctx context.Context) {
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

	invoke := func(t *testing.T, method string) (sent string, body string, code codes.Code) {
		t.Helper()
		resp, err := client.InvokeService(ctx, &rtv1.InvokeServiceRequest{
			Id: r.callee.AppID(),
			Message: &commonv1.InvokeRequest{
				Method:        method,
				HttpExtension: &commonv1.HTTPExtension{Verb: commonv1.HTTPExtension_GET},
			},
		})
		if err != nil {
			return method, status.Convert(err).Message(), status.Convert(err).Code()
		}
		return method, string(resp.GetData().GetValue()), codes.OK
	}

	t.Run("allowed path succeeds", func(t *testing.T) {
		sent, body, code := invoke(t, "public")
		assert.Equalf(t, codes.OK, code, "caller sent %q, callee received: %s", sent, body)
		assert.Equal(t, "public", body)
	})

	t.Run("denied path is rejected", func(t *testing.T) {
		sent, body, code := invoke(t, "admin")
		assert.Equalf(t, codes.Internal.String(), code.String(), "caller sent method %q, callee received: %s", sent, body)
	})

	// gRPC passes the method as a raw string — no URL encoding, no fragment
	// stripping, no query splitting. All characters reach Dapr literally.

	// Methods containing #, ?, or \x00 are rejected by NormalizeMethod before
	// ACL evaluation. The error is wrapped by ErrDirectInvoke as codes.Internal.

	t.Run("hash in method", func(t *testing.T) {
		sent, body, code := invoke(t, "admin#stream")
		assert.NotEqualf(t, codes.OK, code, "caller sent method %q, callee received: %s", sent, body)
	})

	t.Run("question mark in method", func(t *testing.T) {
		sent, body, code := invoke(t, "admin?stream")
		assert.NotEqualf(t, codes.OK, code, "caller sent method %q, callee received: %s", sent, body)
	})

	// NormalizeMethod no longer decodes percent-encoding, so raw % is not
	// forbidden. The method passes through to ACL as "admin%stream" which
	// doesn't match /public → denied by ACL (Internal).
	t.Run("percent in method", func(t *testing.T) {
		sent, body, code := invoke(t, "admin%stream")
		assert.Equalf(t, codes.Internal, code, "caller sent method %q, callee received: %s", sent, body)
	})

	// Plain path traversal is resolved before dispatch and ACL check.
	// admin/../public resolves to "public" which is allowed by ACL.
	t.Run("path traversal to allowed path", func(t *testing.T) {
		sent, body, code := invoke(t, "admin/../public")
		assert.Equalf(t, codes.OK, code, "caller sent method %q, callee received: %s", sent, body)
		assert.Equal(t, "public", body)
	})

	// admin/../../public resolves to "public" (extra ../ is clamped).
	t.Run("double traversal to allowed path", func(t *testing.T) {
		sent, body, code := invoke(t, "admin/../../public")
		assert.Equalf(t, codes.OK, code, "caller sent method %q, callee received: %s", sent, body)
		assert.Equal(t, "public", body)
	})

	// public/../unauthorized resolves to "unauthorized" which is denied by ACL.
	t.Run("traversal to arbitrary path", func(t *testing.T) {
		sent, body, code := invoke(t, "public/../unauthorized")
		assert.Equalf(t, codes.Internal, code, "caller sent method %q, callee received: %s", sent, body)
	})

	// Methods with # are rejected by NormalizeMethod before ACL.
	t.Run("hash hides traversal after allowed prefix", func(t *testing.T) {
		sent, body, code := invoke(t, "public#/../admin")
		assert.NotEqualf(t, codes.OK, code, "caller sent method %q, callee received: %s", sent, body)
	})

	// Methods with ? are rejected by NormalizeMethod before ACL.
	t.Run("question mark hides traversal after allowed prefix", func(t *testing.T) {
		sent, body, code := invoke(t, "public?/../admin")
		assert.NotEqualf(t, codes.OK, code, "caller sent method %q, callee received: %s", sent, body)
	})

	// NormalizeMethod no longer decodes %, so raw % passes through.
	// path.Clean("public%/../admin") → "admin". ACL denies → Internal.
	t.Run("percent hides traversal after allowed prefix", func(t *testing.T) {
		sent, body, code := invoke(t, "public%/../admin")
		assert.Equalf(t, codes.Internal, code, "caller sent method %q, callee received: %s", sent, body)
	})

	t.Run("null byte in method", func(t *testing.T) {
		sent, body, code := invoke(t, "admin\x00public")
		assert.NotEqualf(t, codes.OK, code, "caller sent method %q, callee received: %s", sent, body)
	})

	// NormalizeMethod no longer decodes percent-encoding, so
	// admin%2F..%2Fpublic stays as-is. path.Clean has no / to resolve.
	// ACL sees /admin%2F..%2Fpublic → doesn't match /public → denied.
	t.Run("encoded slash traversal", func(t *testing.T) {
		sent, body, code := invoke(t, "admin%2F..%2Fpublic")
		assert.Equalf(t, codes.Internal, code, "caller sent method %q, callee received: %s", sent, body)
	})

	t.Run("backslash traversal", func(t *testing.T) {
		sent, body, code := invoke(t, `admin\..\public`)
		assert.Equalf(t, codes.Internal, code, "caller sent method %q, callee received: %s", sent, body)
	})

	t.Run("multiple reserved characters", func(t *testing.T) {
		sent, body, code := invoke(t, "admin#?%bypass")
		assert.NotEqualf(t, codes.OK, code, "caller sent method %q, callee received: %s", sent, body)
	})

	// Edge cases: characters/patterns that NormalizeMethod might miss.

	t.Run("backslash traversal to allowed path", func(t *testing.T) {
		// Go's path.Clean doesn't treat \ as separator but Windows backends do.
		sent, body, code := invoke(t, `/admin\..\public`)
		assert.Equalf(t, codes.Internal, code, "caller sent method %q, callee received: %s", sent, body)
	})

	t.Run("semicolon path parameter on allowed path", func(t *testing.T) {
		// Spring/Java strip ;params before routing. If ACL matches /public
		// literally, /public;evil wouldn't match → denied. But Java would
		// route to /public.
		sent, body, code := invoke(t, "/public;../../admin")
		assert.Equalf(t, codes.Internal, code, "caller sent method %q, callee received: %s", sent, body)
	})

	t.Run("tab in method", func(t *testing.T) {
		sent, body, code := invoke(t, "/admin\t/../public")
		assert.NotEqualf(t, codes.OK, code, "caller sent method %q, callee received: %s", sent, body)
	})

	t.Run("newline in method", func(t *testing.T) {
		sent, body, code := invoke(t, "/admin\n/../public")
		assert.NotEqualf(t, codes.OK, code, "caller sent method %q, callee received: %s", sent, body)
	})

	t.Run("carriage return in method", func(t *testing.T) {
		sent, body, code := invoke(t, "/admin\r/../public")
		assert.NotEqualf(t, codes.OK, code, "caller sent method %q, callee received: %s", sent, body)
	})

	t.Run("unicode fullwidth solidus traversal", func(t *testing.T) {
		// U+FF0F (／) looks like / but isn't. Some frameworks normalize it.
		sent, body, code := invoke(t, "/admin\uff0f..\uff0fpublic")
		assert.Equalf(t, codes.Internal, code, "caller sent method %q, callee received: %s", sent, body)
	})

	t.Run("overlong utf8 slash", func(t *testing.T) {
		// %C0%AF is overlong UTF-8 for /. Go doesn't decode it as / but some
		// C-based backends do.
		sent, body, code := invoke(t, "/admin\xc0\xaf..\xc0\xafpublic")
		assert.Equalf(t, codes.Internal, code, "caller sent method %q, callee received: %s", sent, body)
	})

	// NormalizeMethod no longer decodes percent-encoding, so %09/%0A/%0D
	// are treated as literal printable characters. path.Clean resolves ../
	// and the result reaches ACL as /public → allowed.
	t.Run("encoded tab in method", func(t *testing.T) {
		sent, body, code := invoke(t, "/admin%09/../public")
		assert.Equalf(t, codes.OK, code, "caller sent method %q, callee received: %s", sent, body)
		assert.Equal(t, "/public", body, "callee must receive clean path, not raw traversal")
	})

	t.Run("encoded newline in method", func(t *testing.T) {
		sent, body, code := invoke(t, "/admin%0A/../public")
		assert.Equalf(t, codes.OK, code, "caller sent method %q, callee received: %s", sent, body)
		assert.Equal(t, "/public", body, "callee must receive clean path, not raw traversal")
	})

	t.Run("encoded carriage return in method", func(t *testing.T) {
		sent, body, code := invoke(t, "/admin%0D/../public")
		assert.Equalf(t, codes.OK, code, "caller sent method %q, callee received: %s", sent, body)
		assert.Equal(t, "/public", body, "callee must receive clean path, not raw traversal")
	})

	// Percent-encoded slashes (%2F) are literal characters in gRPC methods.
	// NormalizeMethod does not decode percent-encoding, so path.Clean sees
	// no '/' separators and cannot resolve traversal. The ACL must also
	// treat them as literals (not decode them).

	t.Run("encoded slash without traversal", func(t *testing.T) {
		// admin%2Fpublic is a single opaque segment — no path separator.
		// ACL sees /admin%2Fpublic → doesn't match /public → denied.
		sent, body, code := invoke(t, "admin%2Fpublic")
		assert.Equalf(t, codes.Internal, code, "caller sent method %q, callee received: %s", sent, body)
	})

	t.Run("double encoded slash traversal", func(t *testing.T) {
		// admin%2F..%2F..%2Fpublic is opaque — no real slashes for traversal.
		sent, body, code := invoke(t, "admin%2F..%2F..%2Fpublic")
		assert.Equalf(t, codes.Internal, code, "caller sent method %q, callee received: %s", sent, body)
	})

	t.Run("encoded slash from nested denied path", func(t *testing.T) {
		// admin%2Fsecret%2F..%2F..%2Fpublic is opaque.
		sent, body, code := invoke(t, "admin%2Fsecret%2F..%2F..%2Fpublic")
		assert.Equalf(t, codes.Internal, code, "caller sent method %q, callee received: %s", sent, body)
	})

	// Mixed literal and encoded slashes: the literal / is the only path
	// separator. path.Clean resolves ../ relative to real segments.

	t.Run("mixed literal and encoded slash traversal", func(t *testing.T) {
		// admin%2Fsecret/../public → path.Clean sees one real '/' between
		// "admin%2Fsecret" and ".." → resolves to "public" → ACL allows.
		sent, body, code := invoke(t, "admin%2Fsecret/../public")
		assert.Equalf(t, codes.OK, code, "caller sent method %q, callee received: %s", sent, body)
		assert.Equal(t, "public", body)
	})

	t.Run("traversal with encoded slash to denied path", func(t *testing.T) {
		// public/../admin%2Fsecret → resolves to "admin%2Fsecret" → denied.
		sent, body, code := invoke(t, "public/../admin%2Fsecret")
		assert.Equalf(t, codes.Internal, code, "caller sent method %q, callee received: %s", sent, body)
	})

	// Verify callee receives the normalized method, not the raw input.
	t.Run("callee receives normalized method from traversal", func(t *testing.T) {
		sent, body, code := invoke(t, "admin/../public")
		assert.Equalf(t, codes.OK, code, "caller sent method %q, callee received: %s", sent, body)
		// Callee should receive "public", not "admin/../public"
		assert.Equal(t, "public", body)
	})
}
