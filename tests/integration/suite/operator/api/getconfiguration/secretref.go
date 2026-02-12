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
	"encoding/json"
	"fmt"
	"testing"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apiextensionsV1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	commonapi "github.com/dapr/dapr/pkg/apis/common"
	configapi "github.com/dapr/dapr/pkg/apis/configuration/v1alpha1"
	"github.com/dapr/dapr/pkg/config"
	operatorv1pb "github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/kubernetes"
	operator "github.com/dapr/dapr/tests/integration/framework/process/operator"
	procsentry "github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(secretref))
}

// secretref tests that the operator correctly resolves secret references in
// Configuration OTel headers and converts CRD types to runtime-compatible
// internal config types.
type secretref struct {
	sentry  *procsentry.Sentry
	kubeapi *kubernetes.Kubernetes
	op      *operator.Operator
}

func (s *secretref) Setup(t *testing.T) []framework.Option {
	s.sentry = procsentry.New(t, procsentry.WithTrustDomain("integration.test.dapr.io"))

	tracingConfig := configapi.Configuration{
		TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "Configuration"},
		ObjectMeta: metav1.ObjectMeta{Name: "tracing-config", Namespace: "default"},
		Spec: configapi.ConfigurationSpec{
			TracingSpec: &configapi.TracingSpec{
				Otel: &configapi.OtelSpec{
					EndpointAddress: "otel.example.com:4317",
					Protocol:        "grpc",
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
					Timeout: &metav1.Duration{Duration: 30_000_000_000}, // 30s
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
			s.sentry.Port(),
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

	s.op = operator.New(t,
		operator.WithNamespace("default"),
		operator.WithKubeconfigPath(s.kubeapi.KubeconfigPath(t)),
		operator.WithTrustAnchorsFile(s.sentry.TrustAnchorsFile(t)),
	)

	return []framework.Option{
		framework.WithProcesses(s.kubeapi, s.sentry, s.op),
	}
}

func (s *secretref) Run(t *testing.T, ctx context.Context) {
	s.sentry.WaitUntilRunning(t, ctx)
	s.op.WaitUntilRunning(t, ctx)

	client := s.op.Dial(t, ctx, s.sentry, "myapp")

	t.Run("configuration secrets resolved and types converted", func(t *testing.T) {
		resp, err := client.GetConfiguration(ctx, &operatorv1pb.GetConfigurationRequest{
			Namespace: "default",
			Name:      "tracing-config",
		})
		require.NoError(t, err)
		require.NotNil(t, resp)

		// Unmarshal the response into the internal config type to verify
		// the operator correctly converted CRD types to runtime types
		var conf config.Configuration
		err = json.Unmarshal(resp.GetConfiguration(), &conf)
		require.NoError(t, err)

		require.NotNil(t, conf.Spec.TracingSpec.Otel)

		// Headers should be converted to simple "key=value" strings
		require.Len(t, conf.Spec.TracingSpec.Otel.Headers, 2)
		assert.Equal(t, "x-plain-header=plain-value", conf.Spec.TracingSpec.Otel.Headers[0])
		assert.Equal(t, "x-api-key=my-secret-api-key", conf.Spec.TracingSpec.Otel.Headers[1])

		// Timeout should be converted from metav1.Duration to time.Duration
		require.NotNil(t, conf.Spec.TracingSpec.Otel.Timeout)
		assert.Equal(t, "30s", conf.Spec.TracingSpec.Otel.Timeout.String())
	})
}
