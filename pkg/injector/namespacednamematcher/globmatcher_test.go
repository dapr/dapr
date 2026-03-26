/*
Copyright 2025 The Dapr Authors
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

func TestNewGlobMatcher(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantNil   bool
		wantError bool
	}{
		{
			name:    "empty string returns nil",
			input:   "",
			wantNil: true,
		},
		{
			name:    "whitespace only returns nil",
			input:   "   ",
			wantNil: true,
		},
		{
			name:  "simple exact pattern",
			input: "ns:sa",
		},
		{
			name:  "wildcard star in namespace",
			input: "ns-*:sa",
		},
		{
			name:  "wildcard star in both",
			input: "ns-*:sa-*",
		},
		{
			name:  "question mark pattern",
			input: "ns-?:sa-?",
		},
		{
			name:  "character class pattern",
			input: "ns:sa-[abc]*",
		},
		{
			name:  "character range pattern",
			input: "ns:sa-[a-z]*",
		},
		{
			name:  "multiple patterns",
			input: "ns1:sa1,ns2-*:sa2-*",
		},
		{
			name:  "the original user example",
			input: "something-*:foo*bar",
		},
		{
			name:      "missing colon",
			input:     "namespace",
			wantError: true,
		},
		{
			name:      "both empty",
			input:     ":",
			wantError: true,
		},
		{
			name:      "invalid glob syntax in namespace",
			input:     "ns-[:sa",
			wantError: true,
		},
		{
			name:      "invalid glob syntax in sa",
			input:     "ns:sa-[",
			wantError: true,
		},
		{
			name:    "trailing comma produces no error",
			input:   "ns:sa,",
			wantNil: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			matcher, err := NewGlobMatcher(tc.input)
			if tc.wantError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			if tc.wantNil {
				assert.Nil(t, matcher)
			} else {
				assert.NotNil(t, matcher)
			}
		})
	}
}

func TestGlobMatchesNamespacedName(t *testing.T) {
	tests := []struct {
		name      string
		patterns  string
		namespace string
		saName    string
		want      bool
	}{
		{
			name:      "exact match",
			patterns:  "ns:sa",
			namespace: "ns",
			saName:    "sa",
			want:      true,
		},
		{
			name:      "exact match no match",
			patterns:  "ns:sa",
			namespace: "ns",
			saName:    "other",
			want:      false,
		},
		{
			name:      "star wildcard namespace",
			patterns:  "ns-*:sa",
			namespace: "ns-anything",
			saName:    "sa",
			want:      true,
		},
		{
			name:      "star wildcard both",
			patterns:  "ns-*:sa-*",
			namespace: "ns-foo",
			saName:    "sa-bar",
			want:      true,
		},
		{
			name:      "question mark matches single char",
			patterns:  "ns-?:sa",
			namespace: "ns-A",
			saName:    "sa",
			want:      true,
		},
		{
			name:      "question mark does not match multiple chars",
			patterns:  "ns-?:sa",
			namespace: "ns-AB",
			saName:    "sa",
			want:      false,
		},
		{
			name:      "character class match",
			patterns:  "ns:sa-[abc]",
			namespace: "ns",
			saName:    "sa-b",
			want:      true,
		},
		{
			name:      "character class no match",
			patterns:  "ns:sa-[abc]",
			namespace: "ns",
			saName:    "sa-d",
			want:      false,
		},
		{
			name:      "character range match",
			patterns:  "ns:sa-[a-z]",
			namespace: "ns",
			saName:    "sa-m",
			want:      true,
		},
		{
			name:      "character class with star suffix",
			patterns:  "ns:sa-[abc]*",
			namespace: "ns",
			saName:    "sa-alpha",
			want:      true,
		},
		{
			name:      "character class with star suffix no match",
			patterns:  "ns:sa-[abc]*",
			namespace: "ns",
			saName:    "sa-delta",
			want:      false,
		},
		{
			name:      "original user example match",
			patterns:  "something-*:foo*bar",
			namespace: "something-anything",
			saName:    "fooXYZbar",
			want:      true,
		},
		{
			name:      "original user example match empty middle",
			patterns:  "something-*:foo*bar",
			namespace: "something-",
			saName:    "foobar",
			want:      true,
		},
		{
			name:      "original user example no match",
			patterns:  "something-*:foo*bar",
			namespace: "something-x",
			saName:    "foobaz",
			want:      false,
		},
		{
			name:      "multiple patterns first matches",
			patterns:  "ns1:sa1,ns2:sa2",
			namespace: "ns1",
			saName:    "sa1",
			want:      true,
		},
		{
			name:      "multiple patterns second matches",
			patterns:  "ns1:sa1,ns2:sa2",
			namespace: "ns2",
			saName:    "sa2",
			want:      true,
		},
		{
			name:      "multiple patterns none match",
			patterns:  "ns1:sa1,ns2:sa2",
			namespace: "ns3",
			saName:    "sa3",
			want:      false,
		},
		{
			name:      "star matches empty string",
			patterns:  "ns-*:sa",
			namespace: "ns-",
			saName:    "sa",
			want:      true,
		},
		{
			name:      "namespace mismatch",
			patterns:  "ns:sa-*",
			namespace: "other",
			saName:    "sa-anything",
			want:      false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			matcher, err := NewGlobMatcher(tc.patterns)
			require.NoError(t, err)
			require.NotNil(t, matcher)
			assert.Equal(t, tc.want, matcher.MatchesNamespacedName(tc.namespace, tc.saName))
		})
	}
}
