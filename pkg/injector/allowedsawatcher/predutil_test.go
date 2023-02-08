package allowedsawatcher

import (
	"testing"

	"github.com/stretchr/testify/assert"
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
			s:         "namespace:",
			wantError: true,
		},
		{
			name:      "emptyNS",
			s:         ":sa",
			wantError: true,
		},
		{
			name:      "missingColon",
			s:         "namespace",
			wantError: true,
		},
		{
			name:      "simpleExact",
			s:         "ns:sa",
			wantEqual: map[string]*equalPrefixLists{"ns": {equal: []string{"sa"}}},
		},
		{
			name:      "simpleMultipleExact",
			s:         "ns:sa,ns2:sa2",
			wantEqual: map[string]*equalPrefixLists{"ns": {equal: []string{"sa"}}, "ns2": {equal: []string{"sa2"}}},
		},
		{
			name:      "simpleMultipleExactSameNS",
			s:         "ns:sa,ns:sa2",
			wantEqual: map[string]*equalPrefixLists{"ns": {equal: []string{"sa", "sa2"}}},
		},
		{
			name:      "simpleMultipleExactSameNSSameSA",
			s:         "ns:sa,ns:sa",
			wantEqual: map[string]*equalPrefixLists{"ns": {equal: []string{"sa"}}},
		},
		{
			name:      "simpleMultipleExactSameNSSameSASpaces",
			s:         " ns: sa , ns: sa ",
			wantEqual: map[string]*equalPrefixLists{"ns": {equal: []string{"sa"}}},
		},
		{
			name:         "simplePrefixNS",
			s:            "namespace*:sa",
			wantPrefixed: map[string]*equalPrefixLists{"namespace": {equal: []string{"sa"}}},
		},
		{
			name:      "simplePrefixNSWildcarInMiddle",
			s:         "names*pace:sa",
			wantError: true,
		},
		{
			name:      "errPrefixNSTooShort",
			s:         "nam*:sa",
			wantError: true,
		},
		{
			name:      "errPrefixSATooShort",
			s:         "name:sa*",
			wantError: true,
		},
		{
			name:      "errPrefixSAWildcardNotAtEnd",
			s:         "name:sa*sa",
			wantError: true,
		},
		{
			name:      "errPrefixNSForbidden",
			s:         "kube-*:sa",
			wantError: true,
		},
		{
			name:      "errPrefixNSForbidden",
			s:         "kube2-*:sa,kube-*:sa",
			wantError: true,
		},
		{
			name:         "simpleMultiplePrefixNS",
			s:            "namespace*:sa,namespace2*:sa",
			wantPrefixed: map[string]*equalPrefixLists{"namespace": {equal: []string{"sa"}}, "namespace2": {equal: []string{"sa"}}},
		},
		{
			name:      "simplePrefixSA",
			s:         "namespace:service*",
			wantEqual: map[string]*equalPrefixLists{"namespace": {prefix: []string{"service"}}},
		},
		{
			name:      "simplePrefixSA",
			s:         "namespace:service*",
			wantEqual: map[string]*equalPrefixLists{"namespace": {prefix: []string{"service"}}},
		},
		{
			name: "multiple",
			s:    "namespace:service*, namespace:service, namespace:service2*, namespace*:service3, namespace2:service3*, namespace3*:service3*, namespace5:service5",
			wantEqual: map[string]*equalPrefixLists{
				"namespace": {
					prefix: []string{"service", "service2"},
					equal:  []string{"service"},
				},
				"namespace5": {
					equal: []string{"service5"},
				},
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
