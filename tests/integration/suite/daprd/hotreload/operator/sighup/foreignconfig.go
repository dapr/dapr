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

package sighup

import (
	"context"
	"testing"
	"time"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	configapi "github.com/dapr/dapr/pkg/apis/configuration/v1alpha1"
	"github.com/dapr/dapr/pkg/injector/annotations"
	injectorconsts "github.com/dapr/dapr/pkg/injector/consts"
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
	suite.Register(new(foreignconfig))
}

type foreignconfig struct {
	daprd      *daprd.Daprd
	operator   *operator.Operator
	store      *store.Store
	kubeapi    *kubernetes.Kubernetes
	logOut     *log.Log
	daprsystem *configapi.Configuration
}

func (f *foreignconfig) Setup(t *testing.T) []framework.Option {
	snt := sentry.New(t, sentry.WithTrustDomain("integration.test.dapr.io"))

	f.store = store.New(metav1.GroupVersionKind{
		Group:   "dapr.io",
		Version: "v1alpha1",
		Kind:    "Configuration",
	})

	daprsystem := &configapi.Configuration{
		TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "Configuration"},
		ObjectMeta: metav1.ObjectMeta{Name: "daprsystem", Namespace: "default"},
		Spec: configapi.ConfigurationSpec{
			MTLSSpec: &configapi.MTLSSpec{
				ControlPlaneTrustDomain: "integration.test.dapr.io",
				SentryAddress:           snt.Address(),
			},
		},
	}
	f.daprsystem = daprsystem
	f.store.Add(daprsystem)

	pod := &corev1.Pod{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Pod"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "myapp",
			Namespace: "default",
			Labels:    map[string]string{injectorconsts.SidecarInjectedLabel: "true"},
			Annotations: map[string]string{
				annotations.KeyEnabled: "true",
				annotations.KeyAppID:   "myapp",
				annotations.KeyConfig:  "daprsystem",
			},
		},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "daprd"}}},
	}

	f.kubeapi = kubernetes.New(t,
		kubernetes.WithBaseOperatorAPI(t,
			spiffeid.RequireTrustDomainFromString("integration.test.dapr.io"),
			"default",
			snt.Port(),
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
		operator.WithTrustAnchorsFile(snt.TrustAnchorsFile(t)),
	)

	f.logOut = log.New()

	f.daprd = daprd.New(t,
		daprd.WithMode("kubernetes"),
		daprd.WithAppID("myapp"),
		daprd.WithNamespace("default"),
		daprd.WithConfigs("daprsystem"),
		daprd.WithSentryAddress(snt.Address()),
		daprd.WithControlPlaneAddress(f.operator.Address()),
		daprd.WithControlPlaneTrustDomain("integration.test.dapr.io"),
		daprd.WithEnableMTLS(true),
		daprd.WithDisableK8sSecretStore(true),
		daprd.WithExecOptions(
			exec.WithEnvVars(t, "DAPR_TRUST_ANCHORS", string(snt.CABundle().X509.TrustAnchors)),
			exec.WithStdout(f.logOut),
			exec.WithStderr(f.logOut),
		),
	)

	return []framework.Option{
		framework.WithProcesses(snt, f.kubeapi, f.operator, f.daprd),
	}
}

func (f *foreignconfig) Run(t *testing.T, ctx context.Context) {
	f.operator.WaitUntilRunning(t, ctx)
	f.daprd.WaitUntilRunning(t, ctx)

	assert.EventuallyWithT(t, func(ct *assert.CollectT) {
		assert.True(ct, f.logOut.Contains("Starting to watch Configuration updates for SIGHUP reload"))
	}, 10*time.Second, 10*time.Millisecond)

	t.Run("configuration for another app does not trigger SIGHUP", func(t *testing.T) {
		f.logOut.Reset()

		other := &configapi.Configuration{
			TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "Configuration"},
			ObjectMeta: metav1.ObjectMeta{Name: "otherconfig", Namespace: "default"},
			Spec: configapi.ConfigurationSpec{
				TracingSpec: &configapi.TracingSpec{SamplingRate: "1"},
			},
		}
		f.store.Add(other)
		f.kubeapi.Informer().Add(t, other)

		assert.Never(t, func() bool {
			return f.logOut.Contains("Received signal 'hangup'")
		}, 5*time.Second, 100*time.Millisecond,
			"daprd SIGHUP-reloaded for a configuration belonging to another app")
	})

	t.Run("the app's own configuration triggers SIGHUP", func(t *testing.T) {
		f.logOut.Reset()

		updated := f.daprsystem.DeepCopy()
		updated.Spec.TracingSpec = &configapi.TracingSpec{SamplingRate: "1"}
		f.store.Set(updated)
		f.kubeapi.Informer().Modify(t, updated)

		assert.EventuallyWithT(t, func(ct *assert.CollectT) {
			assert.True(ct, f.logOut.Contains("Received signal 'hangup'"),
				"daprd did not SIGHUP-reload for a change to its own configuration")
		}, 15*time.Second, 10*time.Millisecond)
	})
}
