package allowedsawatcher

import (
	"github.com/stretchr/testify/assert"
	"testing"
)

func Test_getNameNamespaces(t *testing.T) {
	tests := []struct {
		name         string
		s            string
		wantPrefixed map[string]*equalPrefixLists
		wantEqual    map[string]*equalPrefixLists
		wantError    bool
	}{
		{
			name:      "emptySA",
			s:         ":namespace",
			wantError: true,
		},
		{
			name:      "emptyNS",
			s:         "sa:",
			wantError: true,
		},
		{
			name:      "missingColon",
			s:         "namespace",
			wantError: true,
		},
		{
			name:      "simpleExact",
			s:         "sa:ns",
			wantEqual: map[string]*equalPrefixLists{"ns": {equal: []string{"sa"}}},
		},
		{
			name:      "simpleMultipleExact",
			s:         "sa:ns,sa2:ns2",
			wantEqual: map[string]*equalPrefixLists{"ns": {equal: []string{"sa"}}, "ns2": {equal: []string{"sa2"}}},
		},
		{
			name:      "simpleMultipleExactSameNS",
			s:         "sa:ns,sa2:ns",
			wantEqual: map[string]*equalPrefixLists{"ns": {equal: []string{"sa", "sa2"}}},
		},
		{
			name:      "simpleMultipleExactSameNSSameSA",
			s:         "sa:ns,sa:ns",
			wantEqual: map[string]*equalPrefixLists{"ns": {equal: []string{"sa"}}},
		},
		{
			name:      "simpleMultipleExactSameNSSameSASpaces",
			s:         " sa : ns , sa: ns ",
			wantEqual: map[string]*equalPrefixLists{"ns": {equal: []string{"sa"}}},
		},
		{
			name:         "simplePrefixNS",
			s:            "sa:namespace*",
			wantPrefixed: map[string]*equalPrefixLists{"namespace": {equal: []string{"sa"}}},
		},
		{
			name:      "errPrefixNSTooShort",
			s:         "sa:name*",
			wantError: true,
		},
		{
			name:      "errPrefixNSForbidden",
			s:         "sa:kube-*",
			wantError: true,
		},
		{
			name:      "errPrefixNSForbidden",
			s:         "sa:kube2-*,sa:kube-*",
			wantError: true,
		},
		{
			name:         "simpleMultiplePrefixNS",
			s:            "sa:namespace*,sa:namespace2*",
			wantPrefixed: map[string]*equalPrefixLists{"namespace": {equal: []string{"sa"}}, "namespace2": {equal: []string{"sa"}}},
		},
		{
			name:      "simplePrefixSA",
			s:         "service*:namespace",
			wantEqual: map[string]*equalPrefixLists{"namespace": {prefix: []string{"service"}}},
		},
		{
			name:      "simplePrefixSA",
			s:         "service*:namespace",
			wantEqual: map[string]*equalPrefixLists{"namespace": {prefix: []string{"service"}}},
		},
		{
			name: "multiple",
			s:    "service*:namespace, service:namespace, service2*:namespace, service3:namespace*, service3*:namespace2, service3*:namespace3*, service5:namespace5",
			wantEqual: map[string]*equalPrefixLists{
				"namespace": {
					prefix: []string{"service", "service2"},
					equal:  []string{"service"}},
				"namespace5": {
					equal: []string{"service5"}},
				"namespace2": {
					prefix: []string{"service3"},
				},
			},
			wantPrefixed: map[string]*equalPrefixLists{
				"namespace": {
					equal: []string{"service3"},
				},
				"namespace3": {
					prefix: []string{"service3"},
				},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotWild, gotExact, err := getNamespaceNames(tc.s)
			if tc.wantError {
				assert.Error(t, err, "expecting error but did not get it")
			} else {
				assert.NoError(t, err, "not expecting error to happen")
			}
			assert.Equalf(t, tc.wantPrefixed, gotWild, "getNamespaceNames(%v)", tc.s)
			assert.Equalf(t, tc.wantEqual, gotExact, "getNamespaceNames(%v)", tc.s)
		})
	}
}
