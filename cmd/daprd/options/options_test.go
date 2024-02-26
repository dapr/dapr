/*
Copyright 2021 The Dapr Authors
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

package options

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/modes"
	"github.com/dapr/dapr/pkg/runtime"
)

func TestAppFlag(t *testing.T) {
	opts, err := New([]string{
		"-app-id", "testapp", // Single dash
		"--app-port", "80",
		"--app-protocol", "http",
		"--metrics-port", strconv.Itoa(10000),
	})
	require.NoError(t, err)
	assert.EqualValues(t, "testapp", opts.AppID)
	assert.EqualValues(t, "80", opts.AppPort)
	assert.EqualValues(t, "http", opts.AppProtocol)
}

func TestStandaloneGlobalConfig(t *testing.T) {
	opts, err := New([]string{
		"--app-id", "testapp",
		"-mode", string(modes.StandaloneMode), // Single dash
		"--config", "../../../pkg/config/testdata/metric_disabled.yaml",
		"--metrics-port", strconv.Itoa(10000),
	})
	require.NoError(t, err)
	assert.EqualValues(t, "testapp", opts.AppID)
	assert.EqualValues(t, string(modes.StandaloneMode), opts.Mode)
	assert.Equal(t, []string{"../../../pkg/config/testdata/metric_disabled.yaml"}, opts.Config)
}

func TestEnableAPILogging(t *testing.T) {
	t.Run("explicitly enabled", func(t *testing.T) {
		opts, err := New([]string{
			"-enable-api-logging", // Single dash
		})
		require.NoError(t, err)
		require.NotNil(t, opts.EnableAPILogging)
		assert.True(t, *opts.EnableAPILogging)
	})
	t.Run("explicitly enabled with true written out", func(t *testing.T) {
		opts, err := New([]string{
			"--enable-api-logging=true",
		})
		require.NoError(t, err)
		require.NotNil(t, opts.EnableAPILogging)
		assert.True(t, *opts.EnableAPILogging)
	})

	t.Run("explicitly disabled", func(t *testing.T) {
		opts, err := New([]string{
			"-enable-api-logging=false", // Single dash
		})
		require.NoError(t, err)
		require.NotNil(t, opts.EnableAPILogging)
		assert.False(t, *opts.EnableAPILogging)
	})

	t.Run("flag is unset", func(t *testing.T) {
		opts, err := New([]string{})
		require.NoError(t, err)
		require.Nil(t, opts.EnableAPILogging)
	})
}

func TestMultipleConfig(t *testing.T) {
	t.Run("config flag not defined", func(t *testing.T) {
		opts, err := New([]string{})
		require.NoError(t, err)
		require.Empty(t, opts.Config)
	})

	t.Run("single config", func(t *testing.T) {
		opts, err := New([]string{
			"--config", "cfg1.yaml",
		})
		require.NoError(t, err)
		require.Equal(t, []string{"cfg1.yaml"}, opts.Config)
	})

	t.Run("comma-separated configs", func(t *testing.T) {
		opts, err := New([]string{
			"-config=cfg1.yaml,cfg2.yaml", // Single dash
		})
		require.NoError(t, err)
		require.Equal(t, []string{"cfg1.yaml", "cfg2.yaml"}, opts.Config)
	})

	t.Run("multiple config flags", func(t *testing.T) {
		opts, err := New([]string{
			"-config=cfg1.yaml",    // Single dash
			"-config", "cfg2.yaml", // Single dash
		})
		require.NoError(t, err)
		require.Equal(t, []string{"cfg1.yaml", "cfg2.yaml"}, opts.Config)
	})

	t.Run("multiple config flags and comma-separated values", func(t *testing.T) {
		opts, err := New([]string{
			"-config=cfg1.yaml", // Single dash
			"--config", "cfg2.yaml,cfg3.yaml",
		})
		require.NoError(t, err)
		require.Equal(t, []string{"cfg1.yaml", "cfg2.yaml", "cfg3.yaml"}, opts.Config)
	})
}

func TestMaxBodySize(t *testing.T) {
	t.Run("No max-body-size", func(t *testing.T) {
		opts, err := New([]string{})
		require.NoError(t, err)

		assert.Equal(t, runtime.DefaultMaxRequestBodySize, opts.MaxRequestSize)
	})

	t.Run("max-body-size is unitless", func(t *testing.T) {
		opts, err := New([]string{
			"--max-body-size", "400",
		})
		require.NoError(t, err)

		assert.Equal(t, 400, opts.MaxRequestSize)
	})

	t.Run("max-body-size with unit", func(t *testing.T) {
		opts, err := New([]string{
			"--max-body-size", "2Mi",
		})
		require.NoError(t, err)

		assert.Equal(t, 2<<20, opts.MaxRequestSize)
	})

	t.Run("dapr-http-max-request-size", func(t *testing.T) {
		opts, err := New([]string{
			"--dapr-http-max-request-size", "2",
		})
		require.NoError(t, err)

		assert.Equal(t, 2<<20, opts.MaxRequestSize)
	})

	t.Run("max-body-size has priority over dapr-http-max-request-size", func(t *testing.T) {
		opts, err := New([]string{
			"--max-body-size", "1Mi",
			"--dapr-http-max-request-size", "2",
		})
		require.NoError(t, err)

		assert.Equal(t, 1<<20, opts.MaxRequestSize)
	})

	t.Run("max-body-size set to 0", func(t *testing.T) {
		opts, err := New([]string{
			"--max-body-size", "0",
		})
		require.NoError(t, err)

		assert.Equal(t, 0, opts.MaxRequestSize)
	})

	t.Run("max-body-size set to -1", func(t *testing.T) {
		opts, err := New([]string{
			"--max-body-size", "-1",
		})
		require.NoError(t, err)

		assert.Equal(t, -1, opts.MaxRequestSize)
	})

	t.Run("dapr-http-max-request-size set to 0", func(t *testing.T) {
		opts, err := New([]string{
			"--dapr-http-max-request-size", "0",
		})
		require.NoError(t, err)

		assert.Equal(t, 0, opts.MaxRequestSize)
	})

	t.Run("dapr-http-max-request-size set to -1", func(t *testing.T) {
		opts, err := New([]string{
			"--dapr-http-max-request-size", "-1",
		})
		require.NoError(t, err)

		assert.Equal(t, -1, opts.MaxRequestSize)
	})

	t.Run("max-body-size is invalid", func(t *testing.T) {
		_, err := New([]string{
			"--max-body-size", "bad",
		})
		require.Error(t, err)
	})
}

func TestReadBufferSize(t *testing.T) {
	t.Run("No read-buffer-size", func(t *testing.T) {
		opts, err := New([]string{})
		require.NoError(t, err)

		assert.Equal(t, runtime.DefaultReadBufferSize, opts.ReadBufferSize)
	})

	t.Run("read-buffer-size is unitless", func(t *testing.T) {
		opts, err := New([]string{
			"--read-buffer-size", "400",
		})
		require.NoError(t, err)

		assert.Equal(t, 400, opts.ReadBufferSize)
	})

	t.Run("read-buffer-size with unit", func(t *testing.T) {
		opts, err := New([]string{
			"--read-buffer-size", "2Ki",
		})
		require.NoError(t, err)

		assert.Equal(t, 2<<10, opts.ReadBufferSize)
	})

	t.Run("dapr-http-read-buffer-size", func(t *testing.T) {
		opts, err := New([]string{
			"--dapr-http-read-buffer-size", "2",
		})
		require.NoError(t, err)

		assert.Equal(t, 2<<10, opts.ReadBufferSize)
	})

	t.Run("read-buffer-size has priority over dapr-http-read-buffer-size", func(t *testing.T) {
		opts, err := New([]string{
			"--read-buffer-size", "1Ki",
			"--dapr-http-read-buffer-size", "2",
		})
		require.NoError(t, err)

		assert.Equal(t, 1<<10, opts.ReadBufferSize)
	})

	t.Run("read-buffer-size set to 0", func(t *testing.T) {
		opts, err := New([]string{
			"--read-buffer-size", "0",
		})
		require.NoError(t, err)

		assert.Equal(t, 0, opts.ReadBufferSize)
	})

	t.Run("read-buffer-size set to -1", func(t *testing.T) {
		opts, err := New([]string{
			"--read-buffer-size", "-1",
		})
		require.NoError(t, err)

		assert.Equal(t, -1, opts.ReadBufferSize)
	})

	t.Run("dapr-http-read-buffer-size set to 0", func(t *testing.T) {
		opts, err := New([]string{
			"--dapr-http-read-buffer-size", "0",
		})
		require.NoError(t, err)

		assert.Equal(t, 0, opts.ReadBufferSize)
	})

	t.Run("dapr-http-read-buffer-size set to -1", func(t *testing.T) {
		opts, err := New([]string{
			"--dapr-http-read-buffer-size", "-1",
		})
		require.NoError(t, err)

		assert.Equal(t, -1, opts.ReadBufferSize)
	})

	t.Run("read-buffer-size is invalid", func(t *testing.T) {
		_, err := New([]string{
			"--read-buffer-size", "bad",
		})
		require.Error(t, err)
	})
}

func TestControlPlaneEnvVar(t *testing.T) {
	t.Run("should default CLI flags if not defined", func(t *testing.T) {
		opts, err := New([]string{})
		require.NoError(t, err)

		assert.EqualValues(t, "localhost", opts.ControlPlaneTrustDomain)
		assert.EqualValues(t, "default", opts.ControlPlaneNamespace)
	})

	t.Run("should use CLI flags if defined", func(t *testing.T) {
		opts, err := New([]string{
			"--control-plane-namespace", "flag-namespace",
			"--control-plane-trust-domain", "flag-trust-domain",
		})
		require.NoError(t, err)

		assert.EqualValues(t, "flag-trust-domain", opts.ControlPlaneTrustDomain)
		assert.EqualValues(t, "flag-namespace", opts.ControlPlaneNamespace)
	})

	t.Run("should use env vars if flags were not defined", func(t *testing.T) {
		t.Setenv("DAPR_CONTROLPLANE_NAMESPACE", "env-namespace")
		t.Setenv("DAPR_CONTROLPLANE_TRUST_DOMAIN", "env-trust-domain")

		opts, err := New([]string{})
		require.NoError(t, err)

		assert.EqualValues(t, "env-trust-domain", opts.ControlPlaneTrustDomain)
		assert.EqualValues(t, "env-namespace", opts.ControlPlaneNamespace)
	})

	t.Run("should priorities CLI flags if both flags and env vars are defined", func(t *testing.T) {
		t.Setenv("DAPR_CONTROLPLANE_NAMESPACE", "env-namespace")
		t.Setenv("DAPR_CONTROLPLANE_TRUST_DOMAIN", "env-trust-domain")

		opts, err := New([]string{
			"--control-plane-namespace", "flag-namespace",
			"--control-plane-trust-domain", "flag-trust-domain",
		})
		require.NoError(t, err)

		assert.EqualValues(t, "flag-trust-domain", opts.ControlPlaneTrustDomain)
		assert.EqualValues(t, "flag-namespace", opts.ControlPlaneNamespace)
	})
}
