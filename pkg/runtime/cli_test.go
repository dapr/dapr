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
	"os"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/dapr/dapr/pkg/sentry/certs"
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

func TestSetEnvVariables(t *testing.T) {
	t.Run("Should set environment variables", func(t *testing.T) {
		variables := map[string]string{
			"ABC_ID":   "123",
			"ABC_PORT": "234",
			"ABC_HOST": "456",
		}

		err := setEnvVariables(variables)

		assert.NoError(t, err)

		for key, value := range variables {
			assert.Equal(t, value, os.Getenv(key))
		}
	})
}

func TestFromFlagsForMTLSConfig(t *testing.T) {
	err := flag.Set("config", "../config/testdata/mtls_config.yaml")
	assert.NoError(t, err)
	err = flag.Set("app-id", "test-app")
	assert.NoError(t, err)
	os.Setenv(certs.TrustAnchorsEnvVar, "testdata")
	os.Setenv(certs.CertChainEnvVar, "testdata")
	os.Setenv(certs.CertKeyEnvVar, "testdata")

	runtime, err := FromFlags()
	assert.NoError(t, err)
	assert.True(t, runtime.runtimeConfig.mtlsEnabled)
	assert.Equal(t, runtime.runtimeConfig.SentryServiceAddress, "localhost:50001")
	assert.True(t, runtime.globalConfig.Spec.MTLSSpec.Enabled)
	assert.Equal(t, runtime.globalConfig.Spec.MTLSSpec.SentryAddress, "localhost:50001")
}
