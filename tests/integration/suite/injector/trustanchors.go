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
	suite.Register(new(trustanchors))
}

// trustanchors verifies that the injected daprd container consumes trust
// anchors from the per-namespace ConfigMap file mount, and retains the
// deprecated literal env var for back compatibility.
type trustanchors struct {
	injector *procinjector.Injector
	sentry   *procsentry.Sentry
}

func (a *trustanchors) Setup(t *testing.T) []framework.Option {
	a.sentry = procsentry.New(t,
		procsentry.WithTrustDomain("integration.test.dapr.io"),
		procsentry.WithNamespace("dapr-system"),
	)
	a.injector = procinjector.New(t,
		procinjector.WithNamespace("dapr-system"),
		procinjector.WithSentry(a.sentry),
	)
	return []framework.Option{
		framework.WithProcesses(a.sentry, a.injector),
	}
}

func (a *trustanchors) Run(t *testing.T, ctx context.Context) {
	a.injector.WaitUntilRunning(t, ctx)

	podBytes := buildPod("dapr-app", map[string]string{
		"dapr.io/enabled": "true",
		"dapr.io/app-id":  "dapr-app",
	})
	review := admissionv1.AdmissionReview{
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
	}

	ar := a.injector.SendAdmission(t, ctx, review)
	require.NotNil(t, ar.Response)
	require.True(t, ar.Response.Allowed)
	require.NotEmpty(t, ar.Response.Patch)

	var ops jsonpatch.Patch
	require.NoError(t, json.Unmarshal(ar.Response.Patch, &ops))

	var (
		foundVolume    bool
		foundMount     bool
		foundFileEnv   bool
		foundLegacyEnv bool
	)
	checkVolume := func(vol corev1.Volume) {
		if vol.Name != "dapr-trust-anchors" {
			return
		}
		require.NotNil(t, vol.ConfigMap)
		assert.Equal(t, "dapr-root-ca.crt", vol.ConfigMap.Name)
		require.NotNil(t, vol.ConfigMap.Optional)
		assert.True(t, *vol.ConfigMap.Optional)
		foundVolume = true
	}

	checkContainer := func(container corev1.Container) {
		if container.Name != "daprd" {
			return
		}
		for _, mount := range container.VolumeMounts {
			if mount.Name == "dapr-trust-anchors" {
				assert.Equal(t, "/var/run/secrets/dapr.io/tls", mount.MountPath)
				assert.True(t, mount.ReadOnly)
				foundMount = true
			}
		}
		for _, env := range container.Env {
			switch env.Name {
			case "DAPR_TRUST_ANCHORS_FILE":
				assert.Equal(t, "/var/run/secrets/dapr.io/tls/ca.crt", env.Value)
				foundFileEnv = true
			case "DAPR_TRUST_ANCHORS":
				assert.Equal(t, string(a.sentry.CABundle().X509.TrustAnchors), env.Value)
				foundLegacyEnv = true
			}
		}
	}

	for _, op := range ops {
		if op.Kind() != "add" {
			continue
		}
		rawValue, ok := op["value"]
		if !ok || rawValue == nil {
			continue
		}
		b := []byte(*rawValue)

		// Depending on the pod's existing spec, volumes and containers are
		// added either as a whole list or as single appended items.
		var vol corev1.Volume
		if err := json.Unmarshal(b, &vol); err == nil {
			checkVolume(vol)
		}
		var vols []corev1.Volume
		if err := json.Unmarshal(b, &vols); err == nil {
			for _, v := range vols {
				checkVolume(v)
			}
		}
		var container corev1.Container
		if err := json.Unmarshal(b, &container); err == nil {
			checkContainer(container)
		}
		var containers []corev1.Container
		if err := json.Unmarshal(b, &containers); err == nil {
			for _, c := range containers {
				checkContainer(c)
			}
		}
	}

	assert.True(t, foundVolume, "patch should add the trust anchors volume")
	assert.True(t, foundMount, "daprd container should mount the trust anchors volume")
	assert.True(t, foundFileEnv, "daprd container should have the trust anchors file env var")
	assert.True(t, foundLegacyEnv, "daprd container should retain the deprecated trust anchors env var")
}
