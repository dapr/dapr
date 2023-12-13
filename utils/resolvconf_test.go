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
		content  string
		expected string
	}{
		{
			content:  "search svc.cluster.local #test comment",
			expected: "svc.cluster.local",
		},
		{
			content:  "search default.svc.cluster.local svc.cluster.local cluster.local",
			expected: "cluster.local",
		},
		{
			content:  "",
			expected: "cluster.local",
		},
	}
	for _, tc := range testCases {
		domain, err := getClusterDomain([]byte(tc.content))
		if err != nil {
			t.Fatalf("get kube cluster domain error:%s", err)
		}
		assert.Equal(t, tc.expected, domain)
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
