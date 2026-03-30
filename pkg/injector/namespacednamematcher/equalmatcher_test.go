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

package namespacednamematcher

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewEqualMatcher(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantNil   bool
		wantError bool
	}{
		{name: "empty string", input: "", wantNil: true},
		{name: "whitespace only", input: "   ", wantNil: true},
		{name: "valid single", input: "ns:sa"},
		{name: "valid multiple", input: "ns1:sa1,ns2:sa2"},
		{name: "missing colon", input: "namespace", wantError: true},
		{name: "both empty", input: ":", wantError: true},
		{name: "trailing comma", input: "ns:sa,"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m, err := NewEqualMatcher(tc.input)
			if tc.wantError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			if tc.wantNil {
				assert.Nil(t, m)
			} else {
				assert.NotNil(t, m)
			}
		})
	}
}

func TestEqualMatcherMatchesNamespacedName(t *testing.T) {
	tests := []struct {
		name      string
		config    string
		namespace string
		saName    string
		want      bool
	}{
		{
			name: "exact match", config: "ns:sa",
			namespace: "ns", saName: "sa", want: true,
		},
		{
			name: "no match wrong name", config: "ns:sa",
			namespace: "ns", saName: "other", want: false,
		},
		{
			name: "no match wrong namespace", config: "ns:sa",
			namespace: "other", saName: "sa", want: false,
		},
		{
			name: "does not do prefix matching", config: "ns:sa",
			namespace: "ns", saName: "sa-extra", want: false,
		},
		{
			name: "multiple entries second matches", config: "ns1:sa1,ns2:sa2",
			namespace: "ns2", saName: "sa2", want: true,
		},
		{
			name: "special chars are literal", config: "ns?:sa[0]",
			namespace: "ns?", saName: "sa[0]", want: true,
		},
		{
			name: "special chars no glob behavior", config: "ns?:sa[0]",
			namespace: "nsX", saName: "sa0", want: false,
		},
		{
			name: "spaces trimmed", config: " ns : sa ",
			namespace: "ns", saName: "sa", want: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m, err := NewEqualMatcher(tc.config)
			require.NoError(t, err)
			require.NotNil(t, m)
			assert.Equal(t, tc.want, m.MatchesNamespacedName(tc.namespace, tc.saName))
		})
	}
}
