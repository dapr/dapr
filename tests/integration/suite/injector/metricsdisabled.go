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

package injector

import (
	"context"
	"encoding/json"
	"testing"

	jsonpatch "github.com/evanphx/json-patch/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	admissionv1 "k8s.io/api/admission/v1"
	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/uuid"

	"github.com/dapr/dapr/tests/integration/framework"
	procinjector "github.com/dapr/dapr/tests/integration/framework/process/injector"
	procsentry "github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(metricsDisabled))
}

// metricsDisabled verifies that dapr.io/enable-metrics is passed to daprd
// both when it is true and when it is false.
type metricsDisabled struct {
	injector *procinjector.Injector
}

func (m *metricsDisabled) Setup(t *testing.T) []framework.Option {
	sentry := procsentry.New(t,
		procsentry.WithTrustDomain("integration.test.dapr.io"),
		procsentry.WithNamespace("dapr-system"),
	)
	m.injector = procinjector.New(t,
		procinjector.WithNamespace("dapr-system"),
		procinjector.WithSentry(sentry),
	)
	return []framework.Option{
		framework.WithProcesses(sentry, m.injector),
	}
}

func (m *metricsDisabled) Run(t *testing.T, ctx context.Context) {
	m.injector.WaitUntilRunning(t, ctx)

	t.Run("enable-metrics false", func(t *testing.T) {
		args := m.daprdArgs(t, ctx, "false")
		assert.Contains(t, args, "--enable-metrics=false")
		assert.NotContains(t, args, "--metrics-port")
	})

	t.Run("enable-metrics true", func(t *testing.T) {
		args := m.daprdArgs(t, ctx, "true")
		assert.Contains(t, args, "--enable-metrics=true")
		assert.Contains(t, args, "--metrics-port")
	})
}

func (m *metricsDisabled) daprdArgs(t *testing.T, ctx context.Context, enableMetrics string) []string {
	t.Helper()

	podBytes := buildPod("dapr-app", map[string]string{
		"dapr.io/enabled":        "true",
		"dapr.io/app-id":         "dapr-app",
		"dapr.io/enable-metrics": enableMetrics,
	})
	ar := m.injector.SendAdmission(t, ctx, admissionv1.AdmissionReview{
		TypeMeta: metav1.TypeMeta{Kind: "AdmissionReview", APIVersion: "admission.k8s.io/v1"},
		Request: &admissionv1.AdmissionRequest{
			UID:       uuid.NewUUID(),
			Kind:      metav1.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"},
			Name:      "dapr-app",
			Namespace: "dapr-system",
			Operation: "CREATE",
			UserInfo:  authenticationv1.UserInfo{Groups: []string{"system:masters"}},
			Object:    runtime.RawExtension{Raw: podBytes},
		},
	})
	require.NotNil(t, ar.Response)
	require.True(t, ar.Response.Allowed)

	patch, err := jsonpatch.DecodePatch(ar.Response.Patch)
	require.NoError(t, err)
	patched, err := patch.Apply(podBytes)
	require.NoError(t, err)

	var pod corev1.Pod
	require.NoError(t, json.Unmarshal(patched, &pod))
	for _, c := range append(pod.Spec.InitContainers, pod.Spec.Containers...) {
		if c.Name == "daprd" {
			return c.Args
		}
	}
	require.Fail(t, "patch should add daprd container")
	return nil
}
