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

package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewServiceAccountMatcherEmpty(t *testing.T) {
	m, err := NewServiceAccountMatcher()
	require.NoError(t, err)
	require.NotNil(t, m)
	assert.False(t, m("any", "any"))
}

func TestNewServiceAccountMatcherSkipsEmptyStrings(t *testing.T) {
	m, err := NewServiceAccountMatcher("", "   ", "ns:sa")
	require.NoError(t, err)
	require.NotNil(t, m)
	assert.True(t, m("ns", "sa"))
}

func TestNewServiceAccountMatcherMultiplePatterns(t *testing.T) {
	m, err := NewServiceAccountMatcher("ns1:sa1", "ns2:sa2", "ns3:sa3")
	require.NoError(t, err)
	require.NotNil(t, m)

	assert.True(t, m("ns1", "sa1"))
	assert.True(t, m("ns2", "sa2"))
	assert.True(t, m("ns3", "sa3"))
	assert.False(t, m("ns4", "sa4"))
}

func TestNewServiceAccountMatcherErrorPropagates(t *testing.T) {
	_, err := NewServiceAccountMatcher("valid:entry", "ns:sa-[invalid")
	require.Error(t, err)
}

func TestServiceAccountMatcherParsing(t *testing.T) {
	tests := []struct {
		name            string
		input           string
		wantAlwaysFalse bool
		wantError       bool
	}{
		{name: "empty string", input: "", wantAlwaysFalse: true},
		{name: "whitespace only", input: "   ", wantAlwaysFalse: true},
		{name: "valid single", input: "ns:sa"},
		{name: "missing colon", input: "namespace", wantError: true},
		{name: "multiple colons", input: "ns:sa:extra", wantError: true},
		{name: "both empty", input: ":", wantError: true},
		{name: "wildcard star", input: "ns-*:sa"},
		{name: "question mark", input: "ns-?:sa-?"},
		{name: "character class", input: "ns:sa-[abc]*"},
		{name: "invalid glob syntax", input: "ns-[:sa", wantError: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m, err := NewServiceAccountMatcher(tc.input)
			if tc.wantError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, m)
			if tc.wantAlwaysFalse {
				assert.False(t, m("any", "any"))
			}
		})
	}
}

func TestServiceAccountMatcherExactMatching(t *testing.T) {
	tests := []struct {
		name      string
		patterns  []string
		namespace string
		saName    string
		want      bool
	}{
		{name: "exact match", patterns: []string{"ns:sa"}, namespace: "ns", saName: "sa", want: true},
		{name: "no match wrong name", patterns: []string{"ns:sa"}, namespace: "ns", saName: "other", want: false},
		{name: "no match wrong namespace", patterns: []string{"ns:sa"}, namespace: "other", saName: "sa", want: false},
		{name: "does not do substring matching", patterns: []string{"ns:sa"}, namespace: "ns", saName: "sa-extra", want: false},
		{name: "multiple entries second matches", patterns: []string{"ns1:sa1", "ns2:sa2"}, namespace: "ns2", saName: "sa2", want: true},
		{name: "spaces trimmed", patterns: []string{" ns : sa "}, namespace: "ns", saName: "sa", want: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m, err := NewServiceAccountMatcher(tc.patterns...)
			require.NoError(t, err)
			require.NotNil(t, m)
			assert.Equal(t, tc.want, m(tc.namespace, tc.saName))
		})
	}
}

func TestServiceAccountMatcherWildcardMatching(t *testing.T) {
	tests := []struct {
		name      string
		patterns  []string
		namespace string
		saName    string
		want      bool
	}{
		{name: "star wildcard namespace", patterns: []string{"ns-*:sa"}, namespace: "ns-anything", saName: "sa", want: true},
		{name: "star wildcard both", patterns: []string{"ns-*:sa-*"}, namespace: "ns-foo", saName: "sa-bar", want: true},
		{name: "star matches empty string", patterns: []string{"ns-*:sa"}, namespace: "ns-", saName: "sa", want: true},
		{name: "question mark matches single char", patterns: []string{"ns-?:sa"}, namespace: "ns-A", saName: "sa", want: true},
		{name: "question mark does not match multiple chars", patterns: []string{"ns-?:sa"}, namespace: "ns-AB", saName: "sa", want: false},
		{name: "character class match", patterns: []string{"ns:sa-[abc]"}, namespace: "ns", saName: "sa-b", want: true},
		{name: "character class no match", patterns: []string{"ns:sa-[abc]"}, namespace: "ns", saName: "sa-d", want: false},
		{name: "character range match", patterns: []string{"ns:sa-[a-z]"}, namespace: "ns", saName: "sa-m", want: true},
		{name: "character class with star suffix", patterns: []string{"ns:sa-[abc]*"}, namespace: "ns", saName: "sa-alpha", want: true},
		{name: "character class with star suffix no match", patterns: []string{"ns:sa-[abc]*"}, namespace: "ns", saName: "sa-delta", want: false},
		{name: "glob in middle of string", patterns: []string{"something-*:foo*bar"}, namespace: "something-anything", saName: "fooXYZbar", want: true},
		{name: "glob with empty middle", patterns: []string{"something-*:foo*bar"}, namespace: "something-", saName: "foobar", want: true},
		{name: "glob no match", patterns: []string{"something-*:foo*bar"}, namespace: "something-x", saName: "foobaz", want: false},
		{name: "namespace mismatch", patterns: []string{"ns:sa-*"}, namespace: "other", saName: "sa-anything", want: false},
		{name: "multiple patterns first matches", patterns: []string{"ns1:sa1", "ns2:sa2"}, namespace: "ns1", saName: "sa1", want: true},
		{name: "multiple patterns second matches", patterns: []string{"ns1:sa1", "ns2:sa2"}, namespace: "ns2", saName: "sa2", want: true},
		{name: "multiple patterns none match", patterns: []string{"ns1:sa1", "ns2:sa2"}, namespace: "ns3", saName: "sa3", want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m, err := NewServiceAccountMatcher(tc.patterns...)
			require.NoError(t, err)
			require.NotNil(t, m)
			assert.Equal(t, tc.want, m(tc.namespace, tc.saName))
		})
	}
}
