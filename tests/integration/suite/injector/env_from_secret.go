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
	"strings"
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
	suite.Register(new(envFromSecret))
}

// envFromSecret verifies that the injector correctly validates and parses
// dapr.io/env-from-secret annotations for both happy and unhappy paths:
// - Valid secret key/name references are properly injected as SecretKeySelector env vars.
// - DAPR_ENV_KEYS remains synchronized and only advertises successfully injected keys.
// - Invalid, empty, or malformed secret references (e.g. empty name/key, colon only)
//   do not produce invalid SecretKeySelectors and do not leak ghost keys into DAPR_ENV_KEYS.
type envFromSecret struct {
	injector *procinjector.Injector
}

func (e *envFromSecret) Setup(t *testing.T) []framework.Option {
	sentry := procsentry.New(t,
		procsentry.WithTrustDomain("integration.test.dapr.io"),
		procsentry.WithNamespace("dapr-system"),
	)
	e.injector = procinjector.New(t,
		procinjector.WithNamespace("dapr-system"),
		procinjector.WithSentry(sentry),
	)
	return []framework.Option{
		framework.WithProcesses(sentry, e.injector),
	}
}

func (e *envFromSecret) Run(t *testing.T, ctx context.Context) {
	e.injector.WaitUntilRunning(t, ctx)

	t.Run("happy path: valid secret references with name and key", func(t *testing.T) {
		container := e.daprdContainer(t, ctx, map[string]string{
			"dapr.io/enabled":         "true",
			"dapr.io/app-id":          "dapr-app",
			"dapr.io/env-from-secret": "DB_PASS=db-secret:password,API_KEY=api-secret:token",
		})

		secretEnvs := getSecretEnvMap(container.Env)
		require.Contains(t, secretEnvs, "DB_PASS")
		assert.Equal(t, "db-secret", secretEnvs["DB_PASS"].Name)
		assert.Equal(t, "password", secretEnvs["DB_PASS"].Key)

		require.Contains(t, secretEnvs, "API_KEY")
		assert.Equal(t, "api-secret", secretEnvs["API_KEY"].Name)
		assert.Equal(t, "token", secretEnvs["API_KEY"].Key)

		daprEnvKeys := getEnvValue(container.Env, "DAPR_ENV_KEYS")
		assert.Contains(t, daprEnvKeys, "DB_PASS")
		assert.Contains(t, daprEnvKeys, "API_KEY")
	})

	t.Run("happy path: single-part secret reference defaults key to secret name", func(t *testing.T) {
		container := e.daprdContainer(t, ctx, map[string]string{
			"dapr.io/enabled":         "true",
			"dapr.io/app-id":          "dapr-app",
			"dapr.io/env-from-secret": "SECRET_TOKEN=vault-secret",
		})

		secretEnvs := getSecretEnvMap(container.Env)
		require.Contains(t, secretEnvs, "SECRET_TOKEN")
		assert.Equal(t, "vault-secret", secretEnvs["SECRET_TOKEN"].Name)
		assert.Equal(t, "vault-secret", secretEnvs["SECRET_TOKEN"].Key)

		daprEnvKeys := getEnvValue(container.Env, "DAPR_ENV_KEYS")
		assert.Contains(t, daprEnvKeys, "SECRET_TOKEN")
	})

	t.Run("happy path: whitespace around keys and values is trimmed", func(t *testing.T) {
		container := e.daprdContainer(t, ctx, map[string]string{
			"dapr.io/enabled":         "true",
			"dapr.io/app-id":          "dapr-app",
			"dapr.io/env-from-secret": "  TRIMMED_SECRET  =  my-secret : my-key  ",
		})

		secretEnvs := getSecretEnvMap(container.Env)
		require.Contains(t, secretEnvs, "TRIMMED_SECRET")
		assert.Equal(t, "my-secret", secretEnvs["TRIMMED_SECRET"].Name)
		assert.Equal(t, "my-key", secretEnvs["TRIMMED_SECRET"].Key)

		daprEnvKeys := getEnvValue(container.Env, "DAPR_ENV_KEYS")
		assert.Contains(t, daprEnvKeys, "TRIMMED_SECRET")
	})

	t.Run("unhappy path: empty secret value does not create invalid secret ref or ghost env key", func(t *testing.T) {
		container := e.daprdContainer(t, ctx, map[string]string{
			"dapr.io/enabled":         "true",
			"dapr.io/app-id":          "dapr-app",
			"dapr.io/env-from-secret": "EMPTY_VAL=",
		})

		assertNoInvalidSecretKeySelectors(t, container.Env)
		assert.NotContains(t, getSecretEnvMap(container.Env), "EMPTY_VAL")
		assert.NotContains(t, getEnvValue(container.Env, "DAPR_ENV_KEYS"), "EMPTY_VAL")
	})

	t.Run("unhappy path: missing secret name or key does not create invalid secret ref", func(t *testing.T) {
		container := e.daprdContainer(t, ctx, map[string]string{
			"dapr.io/enabled":         "true",
			"dapr.io/app-id":          "dapr-app",
			"dapr.io/env-from-secret": "NO_NAME=:somekey,NO_KEY=somename:",
		})

		assertNoInvalidSecretKeySelectors(t, container.Env)
		secretEnvs := getSecretEnvMap(container.Env)
		assert.NotContains(t, secretEnvs, "NO_NAME")
		assert.NotContains(t, secretEnvs, "NO_KEY")

		daprEnvKeys := getEnvValue(container.Env, "DAPR_ENV_KEYS")
		assert.NotContains(t, daprEnvKeys, "NO_NAME")
		assert.NotContains(t, daprEnvKeys, "NO_KEY")
	})

	t.Run("unhappy path: empty colon reference or whitespace only", func(t *testing.T) {
		container := e.daprdContainer(t, ctx, map[string]string{
			"dapr.io/enabled":         "true",
			"dapr.io/app-id":          "dapr-app",
			"dapr.io/env-from-secret": "COLON_ONLY=:,SPACES_COLON= : ,EMPTY_KEY=   ",
		})

		assertNoInvalidSecretKeySelectors(t, container.Env)
		secretEnvs := getSecretEnvMap(container.Env)
		assert.NotContains(t, secretEnvs, "COLON_ONLY")
		assert.NotContains(t, secretEnvs, "SPACES_COLON")
		assert.NotContains(t, secretEnvs, "EMPTY_KEY")

		daprEnvKeys := getEnvValue(container.Env, "DAPR_ENV_KEYS")
		assert.NotContains(t, daprEnvKeys, "COLON_ONLY")
		assert.NotContains(t, daprEnvKeys, "SPACES_COLON")
		assert.NotContains(t, daprEnvKeys, "EMPTY_KEY")
	})

	t.Run("unhappy path: empty env var name is skipped", func(t *testing.T) {
		container := e.daprdContainer(t, ctx, map[string]string{
			"dapr.io/enabled":         "true",
			"dapr.io/app-id":          "dapr-app",
			"dapr.io/env-from-secret": "=secret-name:secret-key",
		})

		assertNoInvalidSecretKeySelectors(t, container.Env)
		assert.NotContains(t, getEnvValue(container.Env, "DAPR_ENV_KEYS"), "secret-name")
	})

	t.Run("mixed: valid secrets injected while invalid secrets are safely discarded without desyncing DAPR_ENV_KEYS", func(t *testing.T) {
		container := e.daprdContainer(t, ctx, map[string]string{
			"dapr.io/enabled": "true",
			"dapr.io/app-id":  "dapr-app",
			"dapr.io/env-from-secret": "VALID1=sec1:key1,INVALID_EMPTY=,VALID2=sec2:key2,INVALID_COLON=:,INVALID_NO_KEY=sec3:,VALID3=sec4",
		})

		assertNoInvalidSecretKeySelectors(t, container.Env)
		secretEnvs := getSecretEnvMap(container.Env)

		// Valid entries must be present
		require.Contains(t, secretEnvs, "VALID1")
		assert.Equal(t, "sec1", secretEnvs["VALID1"].Name)
		assert.Equal(t, "key1", secretEnvs["VALID1"].Key)

		require.Contains(t, secretEnvs, "VALID2")
		assert.Equal(t, "sec2", secretEnvs["VALID2"].Name)
		assert.Equal(t, "key2", secretEnvs["VALID2"].Key)

		require.Contains(t, secretEnvs, "VALID3")
		assert.Equal(t, "sec4", secretEnvs["VALID3"].Name)
		assert.Equal(t, "sec4", secretEnvs["VALID3"].Key)

		// Invalid entries must NOT be present
		assert.NotContains(t, secretEnvs, "INVALID_EMPTY")
		assert.NotContains(t, secretEnvs, "INVALID_COLON")
		assert.NotContains(t, secretEnvs, "INVALID_NO_KEY")

		// DAPR_ENV_KEYS must only contain the valid entries
		daprEnvKeys := getEnvValue(container.Env, "DAPR_ENV_KEYS")
		envKeySlice := strings.Fields(daprEnvKeys)
		assert.Contains(t, envKeySlice, "VALID1")
		assert.Contains(t, envKeySlice, "VALID2")
		assert.Contains(t, envKeySlice, "VALID3")
		assert.NotContains(t, envKeySlice, "INVALID_EMPTY")
		assert.NotContains(t, envKeySlice, "INVALID_COLON")
		assert.NotContains(t, envKeySlice, "INVALID_NO_KEY")
	})
}

func (e *envFromSecret) daprdContainer(t *testing.T, ctx context.Context, annotations map[string]string) corev1.Container {
	t.Helper()

	podBytes := buildPod("dapr-app", annotations)
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

	ar := e.injector.SendAdmission(t, ctx, review)
	require.NotNil(t, ar.Response)
	require.True(t, ar.Response.Allowed)
	require.NotEmpty(t, ar.Response.Patch, "should contain sidecar patch")

	patch, err := jsonpatch.DecodePatch(ar.Response.Patch)
	require.NoError(t, err)
	patched, err := patch.Apply(podBytes)
	require.NoError(t, err)

	var pod corev1.Pod
	require.NoError(t, json.Unmarshal(patched, &pod))

	for _, c := range pod.Spec.Containers {
		if c.Name == "daprd" {
			return c
		}
	}

	require.Fail(t, "patched pod has no daprd container")
	return corev1.Container{}
}

func getSecretEnvMap(envs []corev1.EnvVar) map[string]*corev1.SecretKeySelector {
	res := make(map[string]*corev1.SecretKeySelector)
	for _, env := range envs {
		if env.ValueFrom != nil && env.ValueFrom.SecretKeyRef != nil {
			res[env.Name] = env.ValueFrom.SecretKeyRef
		}
	}
	return res
}

func getEnvValue(envs []corev1.EnvVar, name string) string {
	for _, env := range envs {
		if env.Name == name {
			return env.Value
		}
	}
	return ""
}

// assertNoInvalidSecretKeySelectors verifies that no SecretKeySelector in the injected
// container has an empty Name or Key, which would cause Kubernetes admission rejection.
func assertNoInvalidSecretKeySelectors(t *testing.T, envs []corev1.EnvVar) {
	t.Helper()
	for _, env := range envs {
		if env.ValueFrom != nil && env.ValueFrom.SecretKeyRef != nil {
			assert.NotEmpty(t, env.ValueFrom.SecretKeyRef.Name, "SecretKeySelector.Name must not be empty for env %s", env.Name)
			assert.NotEmpty(t, env.ValueFrom.SecretKeyRef.Key, "SecretKeySelector.Key must not be empty for env %s", env.Name)
		}
	}
}
