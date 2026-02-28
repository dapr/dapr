/*
Copyright 2025 The Dapr Authors
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

package getconfiguration

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apiextensionsV1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	commonapi "github.com/dapr/dapr/pkg/apis/common"
	configapi "github.com/dapr/dapr/pkg/apis/configuration/v1alpha1"
	"github.com/dapr/dapr/tests/integration/framework"
	httpClient "github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/kubernetes"
	"github.com/dapr/dapr/tests/integration/framework/process/operator"
	"github.com/dapr/dapr/tests/integration/framework/process/otel"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/kit/ptr"
)

func init() {
	suite.Register(new(secretref))
}

// secretref tests the full flow:
// - operator resolves secret references in Configuration OTel headers in-place,
// - sends the CRD Configuration type with resolved values,
// - daprd receives the CRD config and converts to internal types,
// - traces are exported to the collector with the correct custom headers.
type secretref struct {
	collector *otel.Collector
	kubeapi   *kubernetes.Kubernetes
	operator  *operator.Operator
	daprd     *daprd.Daprd
}

func (s *secretref) Setup(t *testing.T) []framework.Option {
	sen := sentry.New(t, sentry.WithTrustDomain("integration.test.dapr.io"))
	s.collector = otel.New(t)

	tracingConfig := configapi.Configuration{
		TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "Configuration"},
		ObjectMeta: metav1.ObjectMeta{Name: "tracing-config", Namespace: "default"},
		Spec: configapi.ConfigurationSpec{
			MTLSSpec: &configapi.MTLSSpec{
				ControlPlaneTrustDomain: "integration.test.dapr.io",
				SentryAddress:           sen.Address(),
			},
			TracingSpec: &configapi.TracingSpec{
				SamplingRate: "1",
				Otel: &configapi.OtelSpec{
					EndpointAddress: s.collector.OTLPGRPCAddress(),
					Protocol:        "grpc",
					IsSecure:        ptr.Of(false),
					Headers: []commonapi.NameValuePair{
						{
							Name: "x-plain-header",
							Value: commonapi.DynamicValue{
								JSON: apiextensionsV1.JSON{Raw: []byte(fmt.Sprintf("%q", "plain-value"))},
							},
						},
						{
							Name: "x-api-key",
							SecretKeyRef: commonapi.SecretKeyRef{
								Name: "otel-secret",
								Key:  "token",
							},
						},
					},
					Timeout: &metav1.Duration{Duration: 30 * time.Second},
				},
			},
		},
	}

	otelSecret := corev1.Secret{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "Secret"},
		ObjectMeta: metav1.ObjectMeta{Name: "otel-secret", Namespace: "default"},
		Data: map[string][]byte{
			"token": []byte("my-secret-api-key"),
		},
	}

	s.kubeapi = kubernetes.New(t,
		kubernetes.WithBaseOperatorAPI(t,
			spiffeid.RequireTrustDomainFromString("integration.test.dapr.io"),
			"default",
			sen.Port(),
		),
		kubernetes.WithClusterDaprConfigurationList(t, &configapi.ConfigurationList{
			TypeMeta: metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "ConfigurationList"},
			Items:    []configapi.Configuration{tracingConfig},
		}),
		kubernetes.WithSecretList(t, &corev1.SecretList{
			TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "SecretList"},
			Items:    []corev1.Secret{otelSecret},
		}),
		kubernetes.WithSecretGet(t, &otelSecret),
	)

	s.operator = operator.New(t,
		operator.WithNamespace("default"),
		operator.WithKubeconfigPath(s.kubeapi.KubeconfigPath(t)),
		operator.WithTrustAnchorsFile(sen.TrustAnchorsFile(t)),
	)

	s.daprd = daprd.New(t,
		daprd.WithMode("kubernetes"),
		daprd.WithConfigs("tracing-config"),
		daprd.WithSentryAddress(sen.Address()),
		daprd.WithControlPlaneAddress(s.operator.Address()),
		daprd.WithDisableK8sSecretStore(true),
		daprd.WithEnableMTLS(true),
		daprd.WithNamespace("default"),
		daprd.WithAppID("test-otel-secretref"),
		daprd.WithExecOptions(exec.WithEnvVars(t,
			"DAPR_TRUST_ANCHORS", string(sen.CABundle().X509.TrustAnchors),
		)),
		daprd.WithControlPlaneTrustDomain("integration.test.dapr.io"),
	)

	return []framework.Option{
		framework.WithProcesses(sen, s.collector, s.kubeapi, s.operator, s.daprd),
	}
}

func (s *secretref) Run(t *testing.T, ctx context.Context) {
	s.collector.WaitUntilRunning(t, ctx)
	s.operator.WaitUntilRunning(t, ctx)
	s.daprd.WaitUntilRunning(t, ctx)

	client := httpClient.HTTP(t)

	t.Run("traces exported with resolved secret headers", func(t *testing.T) {
		metaURL := fmt.Sprintf("http://localhost:%d/v1.0/metadata", s.daprd.HTTPPort())
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, metaURL, nil)
		require.NoError(t, err)
		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.NotEmpty(c, s.collector.GetSpans())
		}, time.Second*20, time.Millisecond*10, "should receive spans with custom headers configured")

		// Verify the custom headers were sent in the gRPC metadata
		md := s.collector.GetHeaders()
		assert.Equal(t, []string{"plain-value"}, md.Get("x-plain-header"))
		assert.Equal(t, []string{"my-secret-api-key"}, md.Get("x-api-key"))
	})
}
