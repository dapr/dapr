/*
Copyright 2024 The Dapr Authors
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

package getconfigurations

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/dapr/kit/ptr"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	confapi "github.com/dapr/dapr/pkg/apis/configuration/v1alpha1"
	operatorv1 "github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/kubernetes"
	operator "github.com/dapr/dapr/tests/integration/framework/process/operator"
	procsentry "github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(basicIncreasedCardinality))
}

// basic tests the operator's ListCompontns API.
type basicIncreasedCardinality struct {
	sentry   *procsentry.Sentry
	kubeapi  *kubernetes.Kubernetes
	operator *operator.Operator

	conf1      *confapi.Configuration
	conf1Exp   *confapi.Configuration
	daprsystem *confapi.Configuration
}

func (b *basicIncreasedCardinality) Setup(t *testing.T) []framework.Option {
	b.sentry = procsentry.New(t, procsentry.WithTrustDomain("integration.test.dapr.io"))

	b.conf1 = &confapi.Configuration{
		TypeMeta:   metav1.TypeMeta{Kind: "Configuration", APIVersion: "dapr.io/v1alpha1"},
		ObjectMeta: metav1.ObjectMeta{Name: "myconfig", Namespace: "default"},
		Spec: confapi.ConfigurationSpec{
			MetricSpec: &confapi.MetricSpec{
				Enabled: ptr.Of(true),
				Rules: []confapi.MetricsRule{
					{
						Name: "dapr_runtime_service_invocation_req_sent_total",
						Labels: []confapi.MetricLabel{
							{
								Name:  "method",
								Regex: map[string]string{"orders/": "orders/.+"},
							},
						},
					},
				},
			},
			LoggingSpec: &confapi.LoggingSpec{
				APILogging: &confapi.APILoggingSpec{
					Enabled: ptr.Of(false),
				},
			},
		},
	}
	b.daprsystem = &confapi.Configuration{
		TypeMeta:   metav1.TypeMeta{Kind: "Configuration", APIVersion: "dapr.io/v1alpha1"},
		ObjectMeta: metav1.ObjectMeta{Name: "daprsystem", Namespace: "default"},
		Spec: confapi.ConfigurationSpec{
			MetricSpec: &confapi.MetricSpec{
				HTTP: &confapi.MetricHTTP{
					IncreasedCardinality: ptr.Of(true),
				},
			},
		},
	}
	b.conf1Exp = b.conf1.DeepCopy()
	b.conf1Exp.Spec.MetricSpec.HTTP = &confapi.MetricHTTP{
		IncreasedCardinality: ptr.Of(true),
	}

	// This configuration should not be listed as it is in a different namespace.
	conf3 := &confapi.Configuration{
		TypeMeta:   metav1.TypeMeta{Kind: "Configuration", APIVersion: "dapr.io/v1alpha1"},
		ObjectMeta: metav1.ObjectMeta{Name: "myconfig3", Namespace: "bar"},
		Spec:       confapi.ConfigurationSpec{},
	}

	b.kubeapi = kubernetes.New(t,
		kubernetes.WithBaseOperatorAPI(t,
			spiffeid.RequireTrustDomainFromString("integration.test.dapr.io"),
			"default",
			b.sentry.Port(),
		),
		kubernetes.WithClusterDaprConfigurationList(t, &confapi.ConfigurationList{
			TypeMeta: metav1.TypeMeta{Kind: "ConfigurationList", APIVersion: "dapr.io/v1alpha1"},
			Items:    []confapi.Configuration{*b.conf1, *b.daprsystem, *conf3},
		}),
	)

	b.operator = operator.New(t,
		operator.WithNamespace("default"),
		operator.WithKubeconfigPath(b.kubeapi.KubeconfigPath(t)),
		operator.WithTrustAnchorsFile(b.sentry.TrustAnchorsFile(t)),
	)

	return []framework.Option{
		framework.WithProcesses(b.kubeapi, b.sentry, b.operator),
	}
}

func (b *basicIncreasedCardinality) Run(t *testing.T, ctx context.Context) {
	b.sentry.WaitUntilRunning(t, ctx)
	b.operator.WaitUntilRunning(t, ctx)

	client := b.operator.Dial(t, ctx, b.sentry, "myapp")

	t.Run("GET", func(t *testing.T) {
		var resp *operatorv1.GetConfigurationResponse
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			var err error
			resp, err = client.GetConfiguration(ctx, &operatorv1.GetConfigurationRequest{Namespace: "default", Name: "myconfig"})
			require.NoError(t, err)
			assert.Greater(c, len(resp.GetConfiguration()), 0)
		}, time.Second*20, time.Millisecond*10)

		b1, err := json.Marshal(b.conf1Exp)
		require.NoError(t, err)

		require.True(t, strings.Contains(string(resp.GetConfiguration()), "myconfig"))
		assert.JSONEq(t, string(b1), string(resp.GetConfiguration()))
	})
}
