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

package configurationupdate

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	configapi "github.com/dapr/dapr/pkg/apis/configuration/v1alpha1"
	"github.com/dapr/dapr/pkg/injector/annotations"
	injectorconsts "github.com/dapr/dapr/pkg/injector/consts"
	operatorv1 "github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/kubernetes"
	"github.com/dapr/dapr/tests/integration/framework/process/kubernetes/store"
	"github.com/dapr/dapr/tests/integration/framework/process/operator"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(foreignconfig))
}

type foreignconfig struct {
	sentry   *sentry.Sentry
	store    *store.Store
	kubeapi  *kubernetes.Kubernetes
	operator *operator.Operator
}

func (f *foreignconfig) Setup(t *testing.T) []framework.Option {
	f.sentry = sentry.New(t, sentry.WithTrustDomain("integration.test.dapr.io"))

	f.store = store.New(metav1.GroupVersionKind{
		Group:   "dapr.io",
		Version: "v1alpha1",
		Kind:    "Configuration",
	})

	pod := &corev1.Pod{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Pod"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "myapp",
			Namespace: "default",
			Labels: map[string]string{
				injectorconsts.SidecarInjectedLabel: "true",
			},
			Annotations: map[string]string{
				annotations.KeyEnabled: "true",
				annotations.KeyAppID:   "myapp",
				annotations.KeyConfig:  "myconfig",
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "daprd"}},
		},
	}

	f.kubeapi = kubernetes.New(t,
		kubernetes.WithBaseOperatorAPI(t,
			spiffeid.RequireTrustDomainFromString("integration.test.dapr.io"),
			"default",
			f.sentry.Port(),
		),
		kubernetes.WithClusterDaprConfigurationListFromStore(t, f.store),
		kubernetes.WithClusterPodList(t, &corev1.PodList{
			TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "PodList"},
			Items:    []corev1.Pod{*pod},
		}),
	)

	f.operator = operator.New(t,
		operator.WithNamespace("default"),
		operator.WithKubeconfigPath(f.kubeapi.KubeconfigPath(t)),
		operator.WithTrustAnchorsFile(f.sentry.TrustAnchorsFile(t)),
	)

	return []framework.Option{
		framework.WithProcesses(f.kubeapi, f.sentry, f.operator),
	}
}

func (f *foreignconfig) Run(t *testing.T, ctx context.Context) {
	f.sentry.WaitUntilRunning(t, ctx)
	f.operator.WaitUntilRunning(t, ctx)

	client := f.operator.Dial(t, ctx, f.sentry, "myapp")

	stream, err := client.ConfigurationUpdate(ctx, &operatorv1.ConfigurationUpdateRequest{
		Namespace: "default",
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, stream.CloseSend()) })

	type recv struct {
		event *operatorv1.ConfigurationUpdateEvent
		err   error
	}
	events := make(chan recv)
	go func() {
		for {
			event, rerr := stream.Recv()
			select {
			case events <- recv{event: event, err: rerr}:
			case <-ctx.Done():
				return
			}
			if rerr != nil {
				return
			}
		}
	}()

	t.Run("update to a configuration not assigned to the app is not streamed", func(t *testing.T) {
		other := &configapi.Configuration{
			ObjectMeta: metav1.ObjectMeta{Name: "otherconfig", Namespace: "default", CreationTimestamp: metav1.Time{}},
			Spec: configapi.ConfigurationSpec{
				TracingSpec: &configapi.TracingSpec{SamplingRate: "1"},
			},
		}
		f.store.Add(other)
		f.kubeapi.Informer().Add(t, other)

		select {
		case r := <-events:
			require.NoError(t, r.err)
			var got configapi.Configuration
			require.NoError(t, json.Unmarshal(r.event.GetConfiguration(), &got))
			assert.Failf(t, "operator streamed a configuration that is not assigned to the app",
				"myapp is assigned [myconfig] but received a %s event for %q", r.event.GetType().String(), got.GetName())
		case <-time.After(3 * time.Second):
		}
	})

	t.Run("update to the app's assigned configuration is streamed", func(t *testing.T) {
		mine := &configapi.Configuration{
			ObjectMeta: metav1.ObjectMeta{Name: "myconfig", Namespace: "default", CreationTimestamp: metav1.Time{}},
			Spec: configapi.ConfigurationSpec{
				TracingSpec: &configapi.TracingSpec{SamplingRate: "1"},
			},
		}
		f.store.Add(mine)
		f.kubeapi.Informer().Add(t, mine)

		timeout := time.After(10 * time.Second)
		for {
			select {
			case r := <-events:
				require.NoError(t, r.err)
				var got configapi.Configuration
				require.NoError(t, json.Unmarshal(r.event.GetConfiguration(), &got))
				if got.GetName() == "myconfig" {
					return
				}
			case <-timeout:
				assert.Fail(t, "timed out waiting for an update for the app's assigned configuration")
				return
			}
		}
	})
}
