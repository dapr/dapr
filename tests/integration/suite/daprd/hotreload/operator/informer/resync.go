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

package informer

import (
	"context"
	"testing"
	"time"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	configapi "github.com/dapr/dapr/pkg/apis/configuration/v1alpha1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/log"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/kubernetes"
	"github.com/dapr/dapr/tests/integration/framework/process/kubernetes/store"
	"github.com/dapr/dapr/tests/integration/framework/process/operator"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(resync))
}

type resync struct {
	daprd      *daprd.Daprd
	kubeapi    *kubernetes.Kubernetes
	operator   *operator.Operator
	daprdLog   *log.Log
	sentryAddr string
}

func (r *resync) appConfig() *configapi.Configuration {
	return &configapi.Configuration{
		TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "Configuration"},
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "appconfig", ResourceVersion: "1"},
		Spec: configapi.ConfigurationSpec{
			MTLSSpec: &configapi.MTLSSpec{
				ControlPlaneTrustDomain: "integration.test.dapr.io",
				SentryAddress:           r.sentryAddr,
			},
		},
	}
}

func (r *resync) Setup(t *testing.T) []framework.Option {
	r.daprdLog = log.New()

	snt := sentry.New(t, sentry.WithTrustDomain("integration.test.dapr.io"))
	r.sentryAddr = snt.Address()

	cfgStore := store.New(metav1.GroupVersionKind{
		Group: "dapr.io", Version: "v1alpha1", Kind: "Configuration",
	})
	cfgStore.Add(r.appConfig(), &configapi.Configuration{
		TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "Configuration"},
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "other", ResourceVersion: "1"},
		Spec:       configapi.ConfigurationSpec{TracingSpec: &configapi.TracingSpec{SamplingRate: "0"}},
	})

	r.kubeapi = kubernetes.New(t,
		kubernetes.WithBaseOperatorAPI(t,
			spiffeid.RequireTrustDomainFromString("integration.test.dapr.io"),
			"default",
			snt.Port(),
		),
		kubernetes.WithClusterDaprConfigurationListFromStore(t, cfgStore),
	)

	r.operator = operator.New(t,
		operator.WithNamespace("default"),
		operator.WithKubeconfigPath(r.kubeapi.KubeconfigPath(t)),
		operator.WithTrustAnchorsFile(snt.TrustAnchorsFile(t)),
		// Force the informer to resync every second so the resync drop path is
		// exercised deterministically instead of on the 10h default.
		operator.WithCacheSyncPeriod(time.Second),
	)

	r.daprd = daprd.New(t,
		daprd.WithMode("kubernetes"),
		daprd.WithConfigs("appconfig"),
		daprd.WithSentryAddress(snt.Address()),
		daprd.WithControlPlaneAddress(r.operator.Address()),
		daprd.WithDisableK8sSecretStore(true),
		daprd.WithEnableMTLS(true),
		daprd.WithNamespace("default"),
		daprd.WithControlPlaneTrustDomain("integration.test.dapr.io"),
		daprd.WithExecOptions(
			exec.WithEnvVars(t, "DAPR_TRUST_ANCHORS", string(snt.CABundle().X509.TrustAnchors)),
			exec.WithStdout(r.daprdLog),
			exec.WithStderr(r.daprdLog),
		),
	)

	return []framework.Option{
		framework.WithProcesses(snt, r.kubeapi, r.operator, r.daprd),
	}
}

func (r *resync) Run(t *testing.T, ctx context.Context) {
	r.operator.WaitUntilRunning(t, ctx)
	r.daprd.WaitUntilRunning(t, ctx)

	assert.Never(t, func() bool {
		return r.daprdLog.Contains("UPDATED event: other") ||
			r.daprdLog.Contains("Received signal 'hangup'")
	}, time.Second*15, time.Millisecond*100,
		"expected the operator to drop resync replays so the sidecar never sees the foreign config or restarts")
}
