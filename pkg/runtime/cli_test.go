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

package runtime

import (
	"flag"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/dapr/dapr/pkg/modes"
)

var (
	testAppID       = "testapp"
	testAppPort     = "80"
	testAppProtocol = "http"
	testMetricsPort = 10000
)

func TestParsePlacementAddr(t *testing.T) {
	testCases := []struct {
		addr string
		out  []string
	}{
		{
			addr: "localhost:1020",
			out:  []string{"localhost:1020"},
		},
		{
			addr: "placement1:50005,placement2:50005,placement3:50005",
			out:  []string{"placement1:50005", "placement2:50005", "placement3:50005"},
		},
		{
			addr: "placement1:50005, placement2:50005, placement3:50005",
			out:  []string{"placement1:50005", "placement2:50005", "placement3:50005"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.addr, func(t *testing.T) {
			assert.EqualValues(t, tc.out, parsePlacementAddr(tc.addr))
		})
	}
}

func TestAppFlag(t *testing.T) {
	// reset CommandLine to avoid conflicts from other tests
	flag.CommandLine = flag.NewFlagSet("runtime-flag-test-cmd", flag.ExitOnError)
	// avoid port conflicts from other tests
	testMetricsPort++

	runtime, err := FromFlags([]string{"--app-id", testAppID, "--app-port", testAppPort, "--app-protocol", testAppProtocol, "--metrics-port", strconv.Itoa(testMetricsPort)})
	assert.NoError(t, err)
	assert.EqualValues(t, testAppID, runtime.runtimeConfig.ID)
	assert.EqualValues(t, 80, runtime.runtimeConfig.ApplicationPort)
	assert.EqualValues(t, testAppProtocol, runtime.runtimeConfig.AppConnectionConfig.Protocol)
}

func TestStandaloneGlobalConfig(t *testing.T) {
	// reset CommandLine to avoid conflicts from other tests
	flag.CommandLine = flag.NewFlagSet("runtime-flag-test-cmd", flag.ExitOnError)
	// avoid port conflicts from other tests
	testMetricsPort++

	runtime, err := FromFlags([]string{"--app-id", testAppID, "--mode", string(modes.StandaloneMode), "--config", "../config/testdata/metric_disabled.yaml", "--metrics-port", strconv.Itoa(testMetricsPort)})
	assert.NoError(t, err)
	assert.EqualValues(t, testAppID, runtime.runtimeConfig.ID)
	assert.EqualValues(t, string(modes.StandaloneMode), runtime.runtimeConfig.Mode)
	assert.False(t, runtime.globalConfig.Spec.MetricsSpec.Enabled)
}
