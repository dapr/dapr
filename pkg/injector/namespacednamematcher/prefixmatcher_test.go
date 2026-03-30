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

func TestNewPrefixMatcher(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantNil   bool
		wantError bool
	}{
		{name: "empty string", input: "", wantNil: true},
		{name: "whitespace only", input: "   ", wantNil: true},
		{name: "exact pair", input: "ns:sa"},
		{name: "namespace prefix", input: "ns*:sa"},
		{name: "sa prefix", input: "ns:sa*"},
		{name: "both prefix", input: "ns*:sa*"},
		{name: "wildcard all ns", input: "*:sa*"},
		{name: "wildcard all sa", input: "ns:*"},
		{name: "both empty wildcards rejected", input: "*:*", wantError: true},
		{name: "missing colon", input: "namespace", wantError: true},
		{name: "both empty", input: ":", wantError: true},
		{name: "wildcard in middle of ns", input: "ns*name:sa", wantError: true},
		{name: "wildcard in middle of sa", input: "ns:sa*name", wantError: true},
		{name: "trailing comma", input: "ns*:sa,"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m, err := NewPrefixMatcher(tc.input)
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

func TestPrefixMatcherMatchesNamespacedName(t *testing.T) {
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
			name: "exact no match", config: "ns:sa",
			namespace: "ns", saName: "other", want: false,
		},
		{
			name: "namespace prefix match", config: "ns*:sa",
			namespace: "ns-something", saName: "sa", want: true,
		},
		{
			name: "namespace prefix no match", config: "ns*:sa",
			namespace: "other", saName: "sa", want: false,
		},
		{
			name: "sa prefix match", config: "ns:sa*",
			namespace: "ns", saName: "sa-deploy", want: true,
		},
		{
			name: "sa prefix no match", config: "ns:sa*",
			namespace: "ns", saName: "other", want: false,
		},
		{
			name: "both prefix match", config: "ns*:sa*",
			namespace: "ns-prod", saName: "sa-web", want: true,
		},
		{
			name: "wildcard all sa", config: "ns:*",
			namespace: "ns", saName: "anything", want: true,
		},
		{
			name: "wildcard all ns", config: "*:sa*",
			namespace: "anything", saName: "sa-test", want: true,
		},
		{
			name: "multiple entries first matches", config: "ns1:sa1,ns2*:sa2*",
			namespace: "ns1", saName: "sa1", want: true,
		},
		{
			name: "multiple entries second matches", config: "ns1:sa1,ns2*:sa2*",
			namespace: "ns2-extra", saName: "sa2-extra", want: true,
		},
		{
			name: "multiple entries none match", config: "ns1:sa1,ns2*:sa2*",
			namespace: "ns3", saName: "sa3", want: false,
		},
		{
			name: "special chars in prefix are literal", config: "foo[bar]*:sa",
			namespace: "foo[bar]baz", saName: "sa", want: true,
		},
		{
			name: "special chars not treated as glob", config: "foo[bar]*:sa",
			namespace: "foob", saName: "sa", want: false,
		},
		{
			name: "question mark is literal", config: "ns?name:sa",
			namespace: "ns?name", saName: "sa", want: true,
		},
		{
			name: "question mark not single char wildcard", config: "ns?name:sa",
			namespace: "nsXname", saName: "sa", want: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m, err := NewPrefixMatcher(tc.config)
			require.NoError(t, err)
			require.NotNil(t, m)
			assert.Equal(t, tc.want, m.MatchesNamespacedName(tc.namespace, tc.saName))
		})
	}
}
