/*
Copyright 2022 The Dapr Authors
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

package utils

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetClusterDomain(t *testing.T) {
	testCases := []struct {
		name     string
		content  string
		expected string
	}{
		{
			name:     "standard search list (default namespace)",
			content:  "search default.svc.cluster.local svc.cluster.local cluster.local",
			expected: "cluster.local",
		},
		{
			// A namespace that sorts before "cluster" must still resolve to the
			// cluster domain and not to the namespace-qualified search domain.
			name:     "namespace sorts before cluster",
			content:  "search app.svc.cluster.local svc.cluster.local cluster.local",
			expected: "cluster.local",
		},
		{
			name:     "namespace sorts after cluster",
			content:  "search zoo.svc.cluster.local svc.cluster.local cluster.local",
			expected: "cluster.local",
		},
		{
			name:     "multi-label namespace",
			content:  "search my-app-ns.svc.cluster.local svc.cluster.local cluster.local",
			expected: "cluster.local",
		},
		{
			name:     "custom multi-label cluster domain",
			content:  "search app.svc.k8s.corp.example svc.k8s.corp.example k8s.corp.example",
			expected: "k8s.corp.example",
		},
		{
			name:     "trailing dots on search entries",
			content:  "search app.svc.cluster.local. svc.cluster.local. cluster.local.",
			expected: "cluster.local",
		},
		{
			name:     "unrelated search entries present",
			content:  "search corp.example example.com app.svc.cluster.local svc.cluster.local cluster.local",
			expected: "cluster.local",
		},
		{
			name:     "single svc search entry with comment",
			content:  "search svc.cluster.local #test comment",
			expected: "cluster.local",
		},
		{
			name:     "no kubernetes-shaped suffix falls back to default",
			content:  "search corp.example example.com",
			expected: "cluster.local",
		},
		{
			name:     "no search line falls back to default",
			content:  "",
			expected: "cluster.local",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			domain, err := getClusterDomain([]byte(tc.content))
			assert.NoError(t, err)
			assert.Equal(t, tc.expected, domain)
		})
	}
}

func TestClusterDomainFromCNAME(t *testing.T) {
	const apiSvc = "kubernetes.default.svc"

	testCases := map[string]struct {
		cname    string
		expected string
	}{
		"FQDN with trailing dot": {
			cname:    "kubernetes.default.svc.cluster.local.",
			expected: "cluster.local",
		},
		"FQDN without trailing dot": {
			cname:    "kubernetes.default.svc.cluster.local",
			expected: "cluster.local",
		},
		"custom cluster domain with trailing dot": {
			cname:    "kubernetes.default.svc.my.custom.domain.",
			expected: "my.custom.domain",
		},
		"custom cluster domain without trailing dot": {
			cname:    "kubernetes.default.svc.my.custom.domain",
			expected: "my.custom.domain",
		},
		"CNAME equals apiSvc with trailing dot": {
			cname:    "kubernetes.default.svc.",
			expected: "",
		},
		"CNAME equals apiSvc without trailing dot": {
			cname:    "kubernetes.default.svc",
			expected: "",
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			domain := clusterDomainFromCNAME(apiSvc, tc.cname)
			assert.Equal(t, tc.expected, domain)
		})
	}
}

func TestGetSearchDomains(t *testing.T) {
	testCases := []struct {
		content  string
		expected []string
	}{
		{
			content:  "search svc.cluster.local #test comment",
			expected: []string{"svc.cluster.local"},
		},
		{
			content:  "search default.svc.cluster.local svc.cluster.local cluster.local",
			expected: []string{"default.svc.cluster.local", "svc.cluster.local", "cluster.local"},
		},
		{
			content:  "",
			expected: []string{},
		},
	}
	for _, tc := range testCases {
		domains := getResolvSearchDomains([]byte(tc.content))
		assert.ElementsMatch(t, domains, tc.expected)
	}
}
