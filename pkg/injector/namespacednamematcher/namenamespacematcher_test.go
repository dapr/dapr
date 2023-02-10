/*
Copyright 2023 The Dapr Authors
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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
			name:      "emptyNamespaceAndSA",
			s:         ":",
			wantError: true,
		},
		{
			name:      "emptyPrefixes",
			s:         "*:*",
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
			name:      "errPrefixSAWildcardNotAtEnd",
			s:         "name:sa*sa",
			wantError: true,
		},
		{
			name:      "errPrefixNSWildcardNotAtEnd",
			s:         "nam*e:salsa",
			wantError: true,
		},
		{
			name:      "errPrefixNSNSWildcardNotAtEnd",
			s:         "name*:sal*sa",
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
			name:      "errPreNSForbidden",
			s:         "kube-system:sa,dapr-system:sa",
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
			matcher, err := CreateFromString(tc.s)
			if tc.wantError {
				if assert.Error(t, err, "expecting error but did not get it") {
					return
				}
			} else {
				assert.NoError(t, err, "not expecting error to happen")
			}
			assert.Equalf(t, tc.wantPrefixed, matcher.prefixed, "CreateFromString(%v)", tc.s)
			assert.Equalf(t, tc.wantEqual, matcher.equal, "CreateFromString(%v)", tc.s)
		})
	}
}

func TestEqualPrefixNameNamespaceMatcher_MatchesObject(t *testing.T) {
	for _, tc := range getMatcherTestCases() {
		matcher, err := CreateFromString(tc.namespaceNames)
		if tc.wantError {
			assert.Error(t, err, "expecting error")
			continue
		}
		sa := &corev1.ServiceAccount{
			ObjectMeta: tc.objectMeta,
		}
		t.Run(tc.name, func(t *testing.T) {
			assert.Equalf(t, tc.wantCreate, matcher.MatchesNamespacedName(sa.Namespace, sa.Name), "MatchesObject(%v)", tc.objectMeta)
		})
	}
}

func getMatcherTestCases() []struct {
	name           string
	namespaceNames string
	objectMeta     metav1.ObjectMeta
	wantCreate     bool
	wantError      bool
} {
	tests := []struct {
		name           string
		namespaceNames string
		objectMeta     metav1.ObjectMeta
		wantCreate     bool
		wantError      bool
	}{
		{
			name:           "equalPredicate",
			namespaceNames: "ns:sa",
			objectMeta:     metav1.ObjectMeta{Name: "sa", Namespace: "ns"},
			wantCreate:     true,
			wantError:      false,
		},
		{
			name:           "equalPredicateNoMatch",
			namespaceNames: "ns:sa,ns:sb,ns:sc",
			objectMeta:     metav1.ObjectMeta{Name: "sd", Namespace: "ns"},
			wantCreate:     false,
			wantError:      false,
		},
		{
			name:           "equalPredicateNoMatchWrongNS",
			namespaceNames: "ns:sa,ns:sb,ns:sc",
			objectMeta:     metav1.ObjectMeta{Name: "sd", Namespace: "ns2"},
			wantCreate:     false,
			wantError:      false,
		},
		{
			name:           "equalNamespacePrefixSA",
			namespaceNames: "ns:vc-sa*",
			objectMeta:     metav1.ObjectMeta{Name: "vc-sa-1234", Namespace: "ns"},
			wantCreate:     true,
			wantError:      false,
		},
		{
			name:           "equalNamespacePrefixSABadPrefix",
			namespaceNames: "ns:vc-sa*sa",
			objectMeta:     metav1.ObjectMeta{Name: "vc-sa-1234", Namespace: "ns"},
			wantCreate:     true,
			wantError:      true,
		},
		{
			name:           "equalNamespacePrefixSANoMatch",
			namespaceNames: "ns:vc-sa*",
			objectMeta:     metav1.ObjectMeta{Name: "vc-sb-1234", Namespace: "ns"},
			wantCreate:     false,
			wantError:      false,
		},
		{
			name:           "equalNamespaceMultiplePrefixSA",
			namespaceNames: "ns:vc-sa*,ns:vc-sb*",
			objectMeta:     metav1.ObjectMeta{Name: "vc-sb-1234", Namespace: "ns"},
			wantCreate:     true,
			wantError:      false,
		},
		{
			name:           "prefixNamespaceMultiplePrefixSA",
			namespaceNames: "name*:vc-sa*,name*:vc-sb*",
			objectMeta:     metav1.ObjectMeta{Name: "vc-sb-1234", Namespace: "namespace"},
			wantCreate:     true,
			wantError:      false,
		},
		{
			name:           "prefixNamespaceMultiplePrefixSANoMatch",
			namespaceNames: "name*:vc-sa*,name*:vc-sb*",
			objectMeta:     metav1.ObjectMeta{Name: "vc-sb-1234", Namespace: "namspace"},
			wantCreate:     false,
			wantError:      false,
		},
	}
	return tests
}
