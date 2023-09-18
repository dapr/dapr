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
	"flag"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/dapr/dapr/pkg/modes"
)

func TestAppFlag(t *testing.T) {
	// reset CommandLine to avoid conflicts from other tests
	flag.CommandLine = flag.NewFlagSet("runtime-flag-test-cmd", flag.ExitOnError)

	opts := New([]string{"--app-id", "testapp", "--app-port", "80", "--app-protocol", "http", "--metrics-port", strconv.Itoa(10000)})
	assert.EqualValues(t, "testapp", opts.AppID)
	assert.EqualValues(t, "80", opts.AppPort)
	assert.EqualValues(t, "http", opts.AppProtocol)
}

func TestStandaloneGlobalConfig(t *testing.T) {
	// reset CommandLine to avoid conflicts from other tests
	flag.CommandLine = flag.NewFlagSet("runtime-flag-test-cmd", flag.ExitOnError)

	opts := New([]string{"--app-id", "testapp", "--mode", string(modes.StandaloneMode), "--config", "../../../pkg/config/testdata/metric_disabled.yaml", "--metrics-port", strconv.Itoa(10000)})
	assert.EqualValues(t, "testapp", opts.AppID)
	assert.EqualValues(t, string(modes.StandaloneMode), opts.Mode)
	assert.Equal(t, []string{"../../../pkg/config/testdata/metric_disabled.yaml"}, []string(opts.Config))
}

func TestControlPlaneEnvVar(t *testing.T) {
	t.Run("should default CLI flags if not defined", func(t *testing.T) {
		// reset CommandLine to avoid conflicts from other tests
		flag.CommandLine = flag.NewFlagSet("runtime-flag-test-cmd", flag.ExitOnError)

		opts := New([]string{})

		assert.EqualValues(t, "localhost", opts.ControlPlaneTrustDomain)
		assert.EqualValues(t, "default", opts.ControlPlaneNamespace)
	})

	t.Run("should use CLI flags if defined", func(t *testing.T) {
		// reset CommandLine to avoid conflicts from other tests
		flag.CommandLine = flag.NewFlagSet("runtime-flag-test-cmd", flag.ExitOnError)

		opts := New([]string{"--control-plane-namespace", "flag-namespace", "--control-plane-trust-domain", "flag-trust-domain"})

		assert.EqualValues(t, "flag-trust-domain", opts.ControlPlaneTrustDomain)
		assert.EqualValues(t, "flag-namespace", opts.ControlPlaneNamespace)
	})

	t.Run("should use env vars if flags were not defined", func(t *testing.T) {
		// reset CommandLine to avoid conflicts from other tests
		flag.CommandLine = flag.NewFlagSet("runtime-flag-test-cmd", flag.ExitOnError)
		t.Setenv("DAPR_CONTROLPLANE_NAMESPACE", "env-namespace")
		t.Setenv("DAPR_CONTROLPLANE_TRUST_DOMAIN", "env-trust-domain")

		opts := New([]string{})

		assert.EqualValues(t, "env-trust-domain", opts.ControlPlaneTrustDomain)
		assert.EqualValues(t, "env-namespace", opts.ControlPlaneNamespace)
	})

	t.Run("should priorities CLI flags if both flags and env vars are defined", func(t *testing.T) {
		// reset CommandLine to avoid conflicts from other tests
		flag.CommandLine = flag.NewFlagSet("runtime-flag-test-cmd", flag.ExitOnError)
		t.Setenv("DAPR_CONTROLPLANE_NAMESPACE", "env-namespace")
		t.Setenv("DAPR_CONTROLPLANE_TRUST_DOMAIN", "env-trust-domain")

		opts := New([]string{"--control-plane-namespace", "flag-namespace", "--control-plane-trust-domain", "flag-trust-domain"})

		assert.EqualValues(t, "flag-trust-domain", opts.ControlPlaneTrustDomain)
		assert.EqualValues(t, "flag-namespace", opts.ControlPlaneNamespace)
	})
}
