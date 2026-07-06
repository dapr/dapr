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
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/dapr/dapr/pkg/apis/common"
	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	configapi "github.com/dapr/dapr/pkg/apis/configuration/v1alpha1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
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
	suite.Register(new(secretrotation))
}

// secretrotation tests that a component referencing a Kubernetes secret
// through the built-in kubernetes secret store is hot reloaded when the
// referenced secret changes, without the Component manifest itself changing.
// The operator resolves the secretKeyRef against the current secret value
// when serving components, and daprd's periodic hot-reload reconcile (here
// shortened from its 60s default) detects the changed resolved value, closes
// the component, and re-initializes it with the new value. It also asserts
// the inverse: while the secret is unchanged, the periodic reconcile must not
// reload the component on every tick.
type secretrotation struct {
	daprd       *daprd.Daprd
	kubeapi     *kubernetes.Kubernetes
	operator    *operator.Operator
	secretStore *store.Store
	daprdLog    *log.Log
}

func (s *secretrotation) Setup(t *testing.T) []framework.Option {
	sentry := sentry.New(t, sentry.WithTrustDomain("integration.test.dapr.io"))

	s.daprdLog = log.New()

	// A secret store component whose behaviour observably depends on the
	// value resolved from the Kubernetes secret: the env var prefix. Reading
	// key "SEC" returns the value of the env var "<prefix>SEC", so a reload
	// with a rotated prefix flips the returned value.
	comp := compapi.Component{
		TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "Component"},
		ObjectMeta: metav1.ObjectMeta{Name: "envstore", Namespace: "default"},
		Spec: compapi.ComponentSpec{
			Type:    "secretstores.local.env",
			Version: "v1",
			Metadata: []common.NameValuePair{
				{
					Name: "prefix",
					SecretKeyRef: common.SecretKeyRef{
						Name: "app-secret",
						Key:  "prefix",
					},
				},
			},
		},
	}

	compStore := store.New(metav1.GroupVersionKind{Group: "dapr.io", Version: "v1alpha1", Kind: "Component"})
	compStore.Add(&comp)

	s.secretStore = store.New(metav1.GroupVersionKind{Version: "v1", Kind: "Secret"})
	s.secretStore.Add(&corev1.Secret{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "Secret"},
		ObjectMeta: metav1.ObjectMeta{Name: "app-secret", Namespace: "default"},
		Data:       map[string][]byte{"prefix": []byte("FOO_")},
	})

	s.kubeapi = kubernetes.New(t,
		kubernetes.WithBaseOperatorAPI(t,
			spiffeid.RequireTrustDomainFromString("integration.test.dapr.io"),
			"default",
			sentry.Port(),
		),
		kubernetes.WithClusterDaprConfigurationList(t, &configapi.ConfigurationList{
			Items: []configapi.Configuration{{
				TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "Configuration"},
				ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "daprsystem"},
				Spec: configapi.ConfigurationSpec{
					MTLSSpec: &configapi.MTLSSpec{
						ControlPlaneTrustDomain: "integration.test.dapr.io",
						SentryAddress:           sentry.Address(),
					},
				},
			}},
		}),
		kubernetes.WithClusterDaprComponentListFromStore(t, compStore),
		kubernetes.WithClusterSecretListFromStore(t, s.secretStore),
	)

	s.operator = operator.New(t,
		operator.WithNamespace("default"),
		operator.WithKubeconfigPath(s.kubeapi.KubeconfigPath(t)),
		operator.WithTrustAnchorsFile(sentry.TrustAnchorsFile(t)),
	)

	s.daprd = daprd.New(t,
		daprd.WithMode("kubernetes"),
		daprd.WithConfigs("daprsystem"),
		daprd.WithLogLevel("debug"),
		daprd.WithHotReloadReconcileInterval(time.Second),
		daprd.WithSentryAddress(sentry.Address()),
		daprd.WithControlPlaneAddress(s.operator.Address()),
		daprd.WithDisableK8sSecretStore(true),
		daprd.WithEnableMTLS(true),
		daprd.WithNamespace("default"),
		daprd.WithControlPlaneTrustDomain("integration.test.dapr.io"),
		daprd.WithExecOptions(
			exec.WithEnvVars(t,
				"DAPR_TRUST_ANCHORS", string(sentry.CABundle().X509.TrustAnchors),
				"FOO_SEC", "foovalue",
				"BAR_SEC", "barvalue",
			),
			exec.WithStdout(s.daprdLog),
			exec.WithStderr(s.daprdLog),
		),
	)

	return []framework.Option{
		framework.WithProcesses(sentry, s.kubeapi, s.operator, s.daprd),
	}
}

func (s *secretrotation) Run(t *testing.T, ctx context.Context) {
	s.operator.WaitUntilRunning(t, ctx)
	s.daprd.WaitUntilRunning(t, ctx)

	httpClient := client.HTTP(t)

	readSecret := func(c *assert.CollectT) string {
		getURL := fmt.Sprintf("http://localhost:%d/v1.0/secrets/envstore/SEC", s.daprd.HTTPPort())
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, getURL, nil)
		if !assert.NoError(c, err) {
			return ""
		}
		resp, err := httpClient.Do(req)
		if !assert.NoError(c, err) {
			return ""
		}
		body, err := io.ReadAll(resp.Body)
		assert.NoError(c, err)
		assert.NoError(c, resp.Body.Close())
		if !assert.Equal(c, http.StatusOK, resp.StatusCode) {
			return ""
		}
		return string(body)
	}

	// The component is initialized with the value the operator resolved from
	// the kubernetes secret.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.JSONEq(c, `{"SEC":"foovalue"}`, readSecret(c))
	}, time.Second*20, time.Millisecond*10)

	// While the secret is unchanged, the periodic reconcile must not reload
	// the component. Confirm the reconcile actually runs in the window
	// (interval is 1s), so the assert.Never below is meaningful and not
	// vacuous.
	require.Eventually(t, func() bool {
		return s.daprdLog.Contains("Running scheduled Component reconcile")
	}, time.Second*10, time.Millisecond*10, "periodic reconcile did not run")

	assert.Never(t, func() bool {
		return s.daprdLog.Contains("Closing existing Component to reload")
	}, time.Second*5, time.Millisecond*100,
		"component with unchanged secret was reloaded by the periodic reconcile (churn)")

	// Rotating the secret reloads the component with the new value.
	secret := corev1.Secret{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "Secret"},
		ObjectMeta: metav1.ObjectMeta{Name: "app-secret", Namespace: "default"},
		Data:       map[string][]byte{"prefix": []byte("BAR_")},
	}
	s.secretStore.Set(&secret)
	s.kubeapi.Informer().Modify(t, &secret)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.JSONEq(c, `{"SEC":"barvalue"}`, readSecret(c))
	}, time.Second*30, time.Millisecond*10)

	require.Eventually(t, func() bool {
		return s.daprdLog.Contains("Closing existing Component to reload")
	}, time.Second*10, time.Millisecond*10,
		"expected the component to have been closed and re-initialized")
}
