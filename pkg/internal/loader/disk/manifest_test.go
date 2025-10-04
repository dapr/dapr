/*
Copyright 2024 The Dapr Authors
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

package disk

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsEnvVarSubstitutionEnabled(t *testing.T) {
	tests := []struct {
		name     string
		envValue string
		expected bool
	}{
		{
			name:     "enabled with 'true'",
			envValue: "true",
			expected: true,
		},
		{
			name:     "enabled with 'True'",
			envValue: "True",
			expected: true,
		},
		{
			name:     "enabled with '1'",
			envValue: "1",
			expected: true,
		},
		{
			name:     "disabled with 'false'",
			envValue: "false",
			expected: false,
		},
		{
			name:     "disabled with '0'",
			envValue: "0",
			expected: false,
		},
		{
			name:     "disabled with empty string",
			envValue: "",
			expected: false,
		},
		{
			name:     "disabled with random value",
			envValue: "random",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envValue != "" {
				t.Setenv(envVarTemplateSubstitutionEnabled, tt.envValue)
			}
			result := isEnvVarSubstitutionEnabled()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestSubstituteEnvVars(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		envVars  map[string]string
		expected string
	}{
		{
			name:     "substitute existing env var",
			input:    `value: {{TEST_VAR}}`,
			envVars:  map[string]string{"TEST_VAR": "actual_value"},
			expected: `value: actual_value`,
		},
		{
			name:     "substitute with default when env var exists",
			input:    `value: {{TEST_VAR:default_value}}`,
			envVars:  map[string]string{"TEST_VAR": "actual_value"},
			expected: `value: actual_value`,
		},
		{
			name:     "use default when env var does not exist",
			input:    `value: {{MISSING_VAR:default_value}}`,
			envVars:  map[string]string{},
			expected: `value: default_value`,
		},
		{
			name:     "leave template when env var missing and no default",
			input:    `value: {{MISSING_VAR}}`,
			envVars:  map[string]string{},
			expected: `value: {{MISSING_VAR}}`,
		},
		{
			name:     "multiple substitutions",
			input:    `host: {{DB_HOST:localhost}}, port: {{DB_PORT:5432}}`,
			envVars:  map[string]string{"DB_HOST": "prod-db"},
			expected: `host: prod-db, port: 5432`,
		},
		{
			name:     "empty default value",
			input:    `value: {{EMPTY_VAR:}}`,
			envVars:  map[string]string{},
			expected: `value: `,
		},
		{
			name:     "spaces in env var name",
			input:    `value: {{ SPACED_VAR }}`,
			envVars:  map[string]string{"SPACED_VAR": "trimmed_value"},
			expected: `value: trimmed_value`,
		},
		{
			name:     "no templates",
			input:    `value: normal_value`,
			envVars:  map[string]string{},
			expected: `value: normal_value`,
		},
		{
			name: "complex yaml with multiple vars",
			input: `apiVersion: v1
kind: Component
metadata:
  name: statestore
spec:
  type: state.redis
  metadata:
  - name: host
    value: {{REDIS_HOST:localhost}}
  - name: port
    value: {{REDIS_PORT:6379}}
  - name: password
    value: {{REDIS_PASSWORD}}`,
			envVars: map[string]string{"REDIS_HOST": "redis.example.com", "REDIS_PASSWORD": "secret123"},
			expected: `apiVersion: v1
kind: Component
metadata:
  name: statestore
spec:
  type: state.redis
  metadata:
  - name: host
    value: redis.example.com
  - name: port
    value: 6379
  - name: password
    value: secret123`,
		},
		{
			name:     "colon in default value",
			input:    `value: {{CONNECTION:host:port:db}}`,
			envVars:  map[string]string{},
			expected: `value: host:port:db`,
		},
		{
			name:     "env var with empty value",
			input:    `value: {{EMPTY_ENV}}`,
			envVars:  map[string]string{"EMPTY_ENV": ""},
			expected: `value: `,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set up environment variables
			for key, value := range tt.envVars {
				require.NoError(t, os.Setenv(key, value))
			}
			defer func() {
				// Clean up environment variables
				for key := range tt.envVars {
					os.Unsetenv(key)
				}
			}()

			result := substituteEnvVars([]byte(tt.input))
			assert.Equal(t, tt.expected, string(result))
		})
	}
}

func TestSubstituteEnvVarsEdgeCases(t *testing.T) {
	t.Run("malformed templates are left as-is", func(t *testing.T) {
		inputs := []string{
			`value: {SINGLE_BRACE}`,
			`value: {{UNCLOSED`,
			`value: NOCLOSED}}`,
			`value: {{}}`,
		}

		for _, input := range inputs {
			result := substituteEnvVars([]byte(input))
			assert.Equal(t, input, string(result), "malformed template should be left unchanged")
		}
	})

	t.Run("nested braces", func(t *testing.T) {
		input := `value: {{VAR:{{nested}}}}`
		// The regex should match the first valid pattern
		result := substituteEnvVars([]byte(input))
		// Since VAR doesn't exist, it should use default which is {{nested}}
		assert.Equal(t, `value: {{nested}}`, string(result))
	})
}
