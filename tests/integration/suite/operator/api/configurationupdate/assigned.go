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
	operatorv1 "github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/kubernetes"
	"github.com/dapr/dapr/tests/integration/framework/process/kubernetes/store"
	"github.com/dapr/dapr/tests/integration/framework/process/operator"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(assigned))
}

// assigned tests that each app is only streamed updates for the configuration
// daprd loads: the first entry of a comma separated dapr.io/config annotation,
// and, for a pod not in the operator's cache yet, the one read from the API
// server.
type assigned struct {
	sentry   *sentry.Sentry
	store    *store.Store
	kubeapi  *kubernetes.Kubernetes
	operator *operator.Operator
}

func (a *assigned) Setup(t *testing.T) []framework.Option {
	a.sentry = sentry.New(t, sentry.WithTrustDomain("integration.test.dapr.io"))

	a.store = store.New(metav1.GroupVersionKind{
		Group:   "dapr.io",
		Version: "v1alpha1",
		Kind:    "Configuration",
	})

	a.kubeapi = kubernetes.New(t,
		kubernetes.WithBaseOperatorAPI(t,
			spiffeid.RequireTrustDomainFromString("integration.test.dapr.io"),
			"default",
			a.sentry.Port(),
		),
		kubernetes.WithClusterDaprConfigurationListFromStore(t, a.store),
		// csvapp and noconfigapp are cached, so they must never be read from the
		// API server. lateapp is only returned by a namespaced list, as if the
		// operator's cache lagged.
		kubernetes.WithClusterPodList(t, &corev1.PodList{
			TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "PodList"},
			Items: []corev1.Pod{
				kubernetes.DaprPod("default", "csvapp", "csvapp", "myconfig,otherconfig"),
				kubernetes.DaprPod("default", "noconfigapp", "noconfigapp", ""),
			},
		}),
		kubernetes.WithNamespacedPodListSelector(t, "default", "dapr.io/app-id=lateapp", &corev1.PodList{
			TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "PodList"},
			Items:    []corev1.Pod{kubernetes.DaprPod("default", "lateapp", "lateapp", "myconfig")},
		}),
	)

	a.operator = operator.New(t,
		operator.WithNamespace("default"),
		operator.WithKubeconfigPath(a.kubeapi.KubeconfigPath(t)),
		operator.WithTrustAnchorsFile(a.sentry.TrustAnchorsFile(t)),
	)

	return []framework.Option{
		framework.WithProcesses(a.kubeapi, a.sentry, a.operator),
	}
}

func (a *assigned) Run(t *testing.T, ctx context.Context) {
	a.sentry.WaitUntilRunning(t, ctx)
	a.operator.WaitUntilRunning(t, ctx)

	watch := func(appID string) <-chan string {
		stream, err := a.operator.Dial(t, ctx, a.sentry, appID).ConfigurationUpdate(ctx, &operatorv1.ConfigurationUpdateRequest{
			Namespace: "default",
		})
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, stream.CloseSend()) })

		names := make(chan string)
		go func() {
			for {
				event, rerr := stream.Recv()
				if rerr != nil {
					return
				}
				var got configapi.Configuration
				if json.Unmarshal(event.GetConfiguration(), &got) != nil {
					return
				}
				select {
				case names <- got.GetName():
				case <-ctx.Done():
					return
				}
			}
		}()
		return names
	}
	csv, late, noconfig := watch("csvapp"), watch("lateapp"), watch("noconfigapp")

	add := func(name string) {
		config := &configapi.Configuration{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default", CreationTimestamp: metav1.Time{}},
			Spec:       configapi.ConfigurationSpec{TracingSpec: &configapi.TracingSpec{SamplingRate: "1"}},
		}
		a.store.Add(config)
		a.kubeapi.Informer().Add(t, config)
	}

	t.Run("update to a configuration daprd does not load is not streamed", func(t *testing.T) {
		add("otherconfig")
		select {
		case name := <-csv:
			assert.Failf(t, "csvapp was streamed a configuration daprd does not load", "received %q", name)
		case name := <-late:
			assert.Failf(t, "lateapp was streamed a configuration not assigned to it", "received %q", name)
		case name := <-noconfig:
			assert.Failf(t, "noconfigapp was streamed a configuration", "received %q", name)
		case <-time.After(3 * time.Second):
		}
	})

	t.Run("update to the configuration daprd loads is streamed", func(t *testing.T) {
		add("myconfig")
		for app, ch := range map[string]<-chan string{"csvapp": csv, "lateapp": late} {
			select {
			case name := <-ch:
				assert.Equal(t, "myconfig", name, app)
			case <-time.After(10 * time.Second):
				assert.Fail(t, "timed out waiting for an update", app)
			}
		}
		select {
		case name := <-noconfig:
			assert.Failf(t, "noconfigapp was streamed a configuration", "received %q", name)
		case <-time.After(time.Second):
		}
	})
}
