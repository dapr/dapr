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

package getconfiguration

import (
	"context"
	"testing"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	configapi "github.com/dapr/dapr/pkg/apis/configuration/v1alpha1"
	operatorv1 "github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/kubernetes"
	"github.com/dapr/dapr/tests/integration/framework/process/operator"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(apifallback))
}

// apifallback tests that GetConfiguration resolves the calling app's pod with
// a direct API read, selected by the injector's app ID label, when the pod is
// not in the operator's pod cache.
type apifallback struct {
	sentry   *sentry.Sentry
	operator *operator.Operator
}

func (a *apifallback) Setup(t *testing.T) []framework.Option {
	a.sentry = sentry.New(t, sentry.WithTrustDomain("integration.test.dapr.io"))

	configType := metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "Configuration"}
	configs := &configapi.ConfigurationList{
		TypeMeta: metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "ConfigurationList"},
		Items: []configapi.Configuration{
			{TypeMeta: configType, ObjectMeta: metav1.ObjectMeta{Name: "myconfig", Namespace: "default"}},
			{TypeMeta: configType, ObjectMeta: metav1.ObjectMeta{Name: "otherconfig", Namespace: "default"}},
		},
	}

	// myapp's pod is only returned by a namespaced list, as if the operator's
	// cache lagged. cachedapp's pod is in the cluster list backing the cache, so
	// it must never be read from the API server.
	kubeapi := kubernetes.New(t,
		kubernetes.WithBaseOperatorAPI(t,
			spiffeid.RequireTrustDomainFromString("integration.test.dapr.io"),
			"default",
			a.sentry.Port(),
		),
		kubernetes.WithClusterDaprConfigurationList(t, configs),
		kubernetes.WithClusterPodList(t, &corev1.PodList{
			TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "PodList"},
			Items:    []corev1.Pod{kubernetes.DaprPod("default", "cachedapp", "cachedapp", "myconfig")},
		}),
		kubernetes.WithNamespacedPodListSelector(t, "default", "dapr.io/app-id=myapp", &corev1.PodList{
			TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "PodList"},
			Items:    []corev1.Pod{kubernetes.DaprPod("default", "myapp", "myapp", "myconfig")},
		}),
	)

	a.operator = operator.New(t,
		operator.WithNamespace("default"),
		operator.WithKubeconfigPath(kubeapi.KubeconfigPath(t)),
		operator.WithTrustAnchorsFile(a.sentry.TrustAnchorsFile(t)),
	)

	return []framework.Option{
		framework.WithProcesses(a.sentry, kubeapi, a.operator),
	}
}

func (a *apifallback) Run(t *testing.T, ctx context.Context) {
	a.sentry.WaitUntilRunning(t, ctx)
	a.operator.WaitUntilRunning(t, ctx)

	client := a.operator.Dial(t, ctx, a.sentry, "myapp")
	get := func(config string) error {
		_, err := client.GetConfiguration(ctx, &operatorv1.GetConfigurationRequest{Name: config, Namespace: "default"})
		return err
	}

	t.Run("app whose pod is not cached is resolved from the API server", func(t *testing.T) {
		require.NoError(t, get("myconfig"))
	})

	t.Run("API server read still enforces the assignment", func(t *testing.T) {
		assert.Equal(t, codes.PermissionDenied, status.Code(get("otherconfig")))
	})

	t.Run("cached app is not read from the API server, even when denied", func(t *testing.T) {
		cached := a.operator.Dial(t, ctx, a.sentry, "cachedapp")
		_, err := cached.GetConfiguration(ctx, &operatorv1.GetConfigurationRequest{Name: "myconfig", Namespace: "default"})
		require.NoError(t, err)
		_, err = cached.GetConfiguration(ctx, &operatorv1.GetConfigurationRequest{Name: "otherconfig", Namespace: "default"})
		assert.Equal(t, codes.PermissionDenied, status.Code(err))
	})
}
