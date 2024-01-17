/*
Copyright 2023 The Dapr Authors
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

package secret

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/dapr/components-contrib/secretstores"
	commonapi "github.com/dapr/dapr/pkg/apis/common"
	componentsapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	compsecret "github.com/dapr/dapr/pkg/components/secretstores"
	"github.com/dapr/dapr/pkg/modes"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	"github.com/dapr/dapr/pkg/runtime/meta"
	"github.com/dapr/dapr/pkg/runtime/mock"
	"github.com/dapr/dapr/pkg/runtime/registry"
	"github.com/dapr/dapr/pkg/security/consts"
	"github.com/dapr/kit/logger"
)

func TestProcessResourceSecrets(t *testing.T) {
	createMockBinding := func() *componentsapi.Component {
		return &componentsapi.Component{
			ObjectMeta: metav1.ObjectMeta{
				Name: "mockBinding",
			},
			Spec: componentsapi.ComponentSpec{
				Type:     "bindings.mock",
				Version:  "v1",
				Metadata: []commonapi.NameValuePair{},
			},
		}
	}

	t.Run("Standalone Mode", func(t *testing.T) {
		mockBinding := createMockBinding()
		mockBinding.Spec.Metadata = append(mockBinding.Spec.Metadata, commonapi.NameValuePair{
			Name: "a",
			SecretKeyRef: commonapi.SecretKeyRef{
				Key:  "key1",
				Name: "name1",
			},
		})
		mockBinding.Auth.SecretStore = compsecret.BuiltinKubernetesSecretStore

		sec := New(Options{
			Registry:       registry.New(registry.NewOptions()).SecretStores(),
			ComponentStore: compstore.New(),
			Meta: meta.New(meta.Options{
				ID:   "test",
				Mode: modes.StandaloneMode,
			}),
		})

		m := mock.NewMockKubernetesStore()
		sec.registry.RegisterComponent(
			func(_ logger.Logger) secretstores.SecretStore {
				return m
			},
			compsecret.BuiltinKubernetesSecretStore,
		)

		// add Kubernetes component manually
		require.NoError(t, sec.Init(context.Background(), componentsapi.Component{
			ObjectMeta: metav1.ObjectMeta{
				Name: compsecret.BuiltinKubernetesSecretStore,
			},
			Spec: componentsapi.ComponentSpec{
				Type:    "secretstores.kubernetes",
				Version: "v1",
			},
		}))

		updated, unready := sec.ProcessResource(context.Background(), mockBinding)
		assert.True(t, updated)
		assert.Equal(t, "value1", mockBinding.Spec.Metadata[0].Value.String())
		assert.Empty(t, unready)
	})

	t.Run("Look up name only", func(t *testing.T) {
		mockBinding := createMockBinding()
		mockBinding.Spec.Metadata = append(mockBinding.Spec.Metadata, commonapi.NameValuePair{
			Name: "a",
			SecretKeyRef: commonapi.SecretKeyRef{
				Name: "name1",
			},
		})
		mockBinding.Auth.SecretStore = "mock"

		sec := New(Options{
			Registry:       registry.New(registry.NewOptions()).SecretStores(),
			ComponentStore: compstore.New(),
			Meta: meta.New(meta.Options{
				ID:   "test",
				Mode: modes.KubernetesMode,
			}),
		})

		sec.registry.RegisterComponent(
			func(_ logger.Logger) secretstores.SecretStore {
				return &mock.SecretStore{}
			},
			"mock",
		)

		// initSecretStore appends Kubernetes component even if kubernetes component is not added
		err := sec.Init(context.Background(), componentsapi.Component{
			ObjectMeta: metav1.ObjectMeta{
				Name: "mock",
			},
			Spec: componentsapi.ComponentSpec{
				Type:    "secretstores.mock",
				Version: "v1",
			},
		})
		require.NoError(t, err)

		updated, unready := sec.ProcessResource(context.Background(), mockBinding)
		assert.True(t, updated)
		assert.Equal(t, "value1", mockBinding.Spec.Metadata[0].Value.String())
		assert.Empty(t, unready)
	})

	t.Run("Secret from env", func(t *testing.T) {
		t.Setenv("MY_ENV_VAR", "ciao mondo")

		mockBinding := createMockBinding()
		mockBinding.Spec.Metadata = append(mockBinding.Spec.Metadata, commonapi.NameValuePair{
			Name:   "a",
			EnvRef: "MY_ENV_VAR",
		})

		sec := New(Options{
			Registry:       registry.New(registry.NewOptions()).SecretStores(),
			ComponentStore: compstore.New(),
			Meta: meta.New(meta.Options{
				ID:   "test",
				Mode: modes.StandaloneMode,
			}),
		})

		updated, unready := sec.ProcessResource(context.Background(), mockBinding)
		assert.True(t, updated)
		assert.Equal(t, "ciao mondo", mockBinding.Spec.Metadata[0].Value.String())
		assert.Empty(t, unready)
	})

	t.Run("Disallowed env var", func(t *testing.T) {
		t.Setenv("APP_API_TOKEN", "test")
		t.Setenv("DAPR_KEY", "test")

		mockBinding := createMockBinding()
		mockBinding.Spec.Metadata = append(mockBinding.Spec.Metadata,
			commonapi.NameValuePair{
				Name:   "a",
				EnvRef: "DAPR_KEY",
			},
			commonapi.NameValuePair{
				Name:   "b",
				EnvRef: "APP_API_TOKEN",
			},
		)

		sec := New(Options{
			Registry:       registry.New(registry.NewOptions()).SecretStores(),
			ComponentStore: compstore.New(),
			Meta: meta.New(meta.Options{
				ID:   "test",
				Mode: modes.StandaloneMode,
			}),
		})

		updated, unready := sec.ProcessResource(context.Background(), mockBinding)
		assert.True(t, updated)
		assert.Equal(t, "", mockBinding.Spec.Metadata[0].Value.String())
		assert.Equal(t, "", mockBinding.Spec.Metadata[1].Value.String())
		assert.Empty(t, unready)
	})
}

func TestIsEnvVarAllowed(t *testing.T) {
	t.Run("no allowlist", func(t *testing.T) {
		tests := []struct {
			name string
			key  string
			want bool
		}{
			{name: "empty string is not allowed", key: "", want: false},
			{name: "key is allowed", key: "FOO", want: true},
			{name: "keys starting with DAPR_ are denied", key: "DAPR_TEST", want: false},
			{name: "APP_API_TOKEN is denied", key: "APP_API_TOKEN", want: false},
			{name: "keys with a space are denied", key: "FOO BAR", want: false},
			{name: "case insensitive app_api_token", key: "app_api_token", want: false},
			{name: "case insensitive dapr_foo", key: "dapr_foo", want: false},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				if got := isEnvVarAllowed(tt.key); got != tt.want {
					t.Errorf("isEnvVarAllowed(%q) = %v, want %v", tt.key, got, tt.want)
				}
			})
		}
	})

	t.Run("with allowlist", func(t *testing.T) {
		t.Setenv(consts.EnvKeysEnvVar, "FOO BAR TEST")

		tests := []struct {
			name string
			key  string
			want bool
		}{
			{name: "FOO is allowed", key: "FOO", want: true},
			{name: "BAR is allowed", key: "BAR", want: true},
			{name: "TEST is allowed", key: "TEST", want: true},
			{name: "FO is not allowed", key: "FO", want: false},
			{name: "EST is not allowed", key: "EST", want: false},
			{name: "BA is not allowed", key: "BA", want: false},
			{name: "AR is not allowed", key: "AR", want: false},
			{name: "keys starting with DAPR_ are denied", key: "DAPR_TEST", want: false},
			{name: "APP_API_TOKEN is denied", key: "APP_API_TOKEN", want: false},
			{name: "keys with a space are denied", key: "FOO BAR", want: false},
			{name: "case insensitive allowlist", key: "foo", want: true},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				if got := isEnvVarAllowed(tt.key); got != tt.want {
					t.Errorf("isEnvVarAllowed(%q) = %v, want %v", tt.key, got, tt.want)
				}
			})
		}
	})
}
