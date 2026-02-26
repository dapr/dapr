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
	"os"
	"path/filepath"
	"testing"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	configapi "github.com/dapr/dapr/pkg/apis/configuration/v1alpha1"
	"github.com/dapr/dapr/pkg/proto/common/v1"
	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/app"
	"github.com/dapr/dapr/tests/integration/framework/process/kubernetes"
	"github.com/dapr/dapr/tests/integration/framework/process/operator"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(trustdomain))
}

type trustdomain struct {
	daprd1       *daprd.Daprd
	daprd2       *daprd.Daprd
	sentry       *sentry.Sentry
	trustAnchors []byte
}

func (e *trustdomain) Setup(t *testing.T) []framework.Option {
	e.sentry = sentry.New(t)

	bundle := e.sentry.CABundle()
	e.trustAnchors = bundle.X509.TrustAnchors
	taFile := filepath.Join(t.TempDir(), "ca.pem")
	require.NoError(t, os.WriteFile(taFile, bundle.X509.TrustAnchors, 0o600))

	placement := placement.New(t,
		placement.WithEnableTLS(true),
		placement.WithTrustAnchorsFile(taFile),
		placement.WithSentryAddress(e.sentry.Address()),
	)

	scheduler := scheduler.New(t,
		scheduler.WithSentry(e.sentry),
		scheduler.WithID("dapr-scheduler-server-0"),
	)

	cnf := configapi.Configuration{
		TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "Configuration"},
		ObjectMeta: metav1.ObjectMeta{Name: "my-config", Namespace: "default"},
		Spec: configapi.ConfigurationSpec{
			NameResolutionSpec: &configapi.NameResolutionSpec{
				Component: "mdns",
			},
			MTLSSpec: &configapi.MTLSSpec{
				ControlPlaneTrustDomain: "localhost",
				SentryAddress:           e.sentry.Address(),
			},
			AccessControlSpec: &configapi.AccessControlSpec{
				TrustDomain: "helloworld",
			},
		},
	}

	kubeapi := kubernetes.New(t,
		kubernetes.WithBaseOperatorAPI(t,
			spiffeid.RequireTrustDomainFromString("localhost"),
			"default",
			e.sentry.Port(),
		),
		kubernetes.WithClusterDaprConfigurationList(t, &configapi.ConfigurationList{
			TypeMeta: metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "ConfigurationList"},
			Items:    []configapi.Configuration{cnf},
		}),
	)

	operator := operator.New(t,
		operator.WithNamespace("default"),
		operator.WithKubeconfigPath(kubeapi.KubeconfigPath(t)),
		operator.WithTrustAnchorsFile(e.sentry.TrustAnchorsFile(t)),
	)

	app := app.New(t)

	e.daprd1 = daprd.New(t,
		daprd.WithAppProtocol("grpc"),
		daprd.WithConfigs("my-config"),
		daprd.WithAppPort(app.Port(t)),
		daprd.WithExecOptions(exec.WithEnvVars(t, "DAPR_TRUST_ANCHORS", string(bundle.X509.TrustAnchors))),
		daprd.WithSentryAddress(e.sentry.Address()),
		daprd.WithEnableMTLS(true),
		daprd.WithMode("kubernetes"),
		daprd.WithPlacementAddresses(placement.Address()),
		daprd.WithSchedulerAddresses(scheduler.Address()),
		daprd.WithControlPlaneAddress(operator.Address()),
		daprd.WithDisableK8sSecretStore(true),
		daprd.WithNamespace("default"),
	)

	e.daprd2 = daprd.New(t,
		daprd.WithAppProtocol("grpc"),
		daprd.WithConfigs("my-config"),
		daprd.WithAppPort(app.Port(t)),
		daprd.WithExecOptions(exec.WithEnvVars(t, "DAPR_TRUST_ANCHORS", string(bundle.X509.TrustAnchors))),
		daprd.WithSentryAddress(e.sentry.Address()),
		daprd.WithEnableMTLS(true),
		daprd.WithControlPlaneAddress(operator.Address()),
		daprd.WithPlacementAddresses(placement.Address()),
		daprd.WithSchedulerAddresses(scheduler.Address()),
		daprd.WithMode("kubernetes"),
		daprd.WithDisableK8sSecretStore(true),
		daprd.WithNamespace("default"),
	)

	return []framework.Option{
		framework.WithProcesses(app, e.sentry, kubeapi, placement, scheduler, operator, e.daprd1, e.daprd2),
	}
}

func (e *trustdomain) Run(t *testing.T, ctx context.Context) {
	e.sentry.WaitUntilRunning(t, ctx)
	e.daprd1.WaitUntilRunning(t, ctx)
	e.daprd2.WaitUntilRunning(t, ctx)

	_, err := e.daprd2.GRPCClient(t, ctx).InvokeService(ctx, &rtv1.InvokeServiceRequest{
		Id: e.daprd1.AppID(),
		Message: &common.InvokeRequest{
			Method: "hello",
		},
	})
	require.NoError(t, err)
}
