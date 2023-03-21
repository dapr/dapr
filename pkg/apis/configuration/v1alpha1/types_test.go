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

package v1alpha1

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/kit/ptr"
)

func TestConfigurationSpecSerialization(t *testing.T) {
	// Load test data
	testDataJSON, loadErr := os.ReadFile(filepath.Join("testdata", "serialization.json"))
	require.NoError(t, loadErr, "failed to read test data file")
	var testData map[string]json.RawMessage
	loadErr = json.Unmarshal(testDataJSON, &testData)
	require.NoError(t, loadErr, "failed to unmarshal test data")

	// Run tests
	tests := []struct {
		// Test name
		name string
		// ConfigurationSpec object to serialize
		obj ConfigurationSpec
		// Key from the testData map
		resultName string
		// Performs various assertions
		assertFn func(t *testing.T, spec *ConfigurationSpec)
	}{
		{
			name:       "empty spec",
			obj:        ConfigurationSpec{},
			resultName: "empty",
			assertFn:   assertMTLSfunc(true),
		},
		{
			name: "mtls: defined but empty",
			obj: ConfigurationSpec{
				MTLSSpec: &MTLSSpec{},
			},
			resultName: "mtls-empty",
			assertFn:   assertMTLSfunc(true),
		},
		{
			name: "mtls: mTLS enabled",
			obj: ConfigurationSpec{
				MTLSSpec: &MTLSSpec{
					Enabled: ptr.Of(true),
				},
			},
			resultName: "mtls-enabled",
			assertFn:   assertMTLSfunc(true),
		},
		{
			name: "mtls: mTLS disabled",
			obj: ConfigurationSpec{
				MTLSSpec: &MTLSSpec{
					Enabled: ptr.Of(false),
				},
			},
			resultName: "mtls-disabled",
			assertFn:   assertMTLSfunc(false),
		},
		{
			name: "metric: defined but empty",
			obj: ConfigurationSpec{
				MetricSpec: &MetricSpec{},
			},
			resultName: "metric-empty",
		},
		{
			name: "metric: metrics enabled",
			obj: ConfigurationSpec{
				MetricSpec: &MetricSpec{
					Enabled: ptr.Of(true),
					Rules: []MetricsRule{
						{Name: "myrule", Labels: []MetricLabel{{Name: "mylabel", Regex: map[string]string{"foo": "bar"}}}},
					},
				},
			},
			resultName: "metric-enabled",
		},
		{
			name: "metric: metrics disabled",
			obj: ConfigurationSpec{
				MetricSpec: &MetricSpec{
					Enabled: ptr.Of(false),
				},
			},
			resultName: "metric-disabled",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Encode the object
			enc, err := json.Marshal(&tt.obj)
			require.NoError(t, err, "failed to marshal object")

			t.Log(tt.resultName, " -- ", string(enc))

			// Compact the expected result
			expectOrig := testData[tt.resultName]
			require.NotEmpty(t, expectOrig, "could not find expected result in test data for name "+tt.resultName)
			var expectBuf bytes.Buffer
			err = json.Compact(&expectBuf, expectOrig)
			require.NoError(t, err, "failed to compact expected result")

			// Check result
			require.Equal(t, expectBuf.String(), string(enc))

			// Decode the object
			var dec ConfigurationSpec
			err = json.Unmarshal(expectOrig, &dec)
			require.NoError(t, err, "failed to unmarshal object")

			if tt.assertFn != nil {
				tt.assertFn(t, &dec)
			}
		})
	}
}

func assertMTLSfunc(expect bool) func(t *testing.T, spec *ConfigurationSpec) {
	if expect {
		return func(t *testing.T, spec *ConfigurationSpec) {
			t.Run("mTlS is enabled", func(t *testing.T) {
				assert.True(t, spec.MTLSSpec.GetEnabled())
			})
		}
	}

	return func(t *testing.T, spec *ConfigurationSpec) {
		t.Run("mTlS is disabled", func(t *testing.T) {
			assert.False(t, spec.MTLSSpec.GetEnabled())
		})
	}
}
