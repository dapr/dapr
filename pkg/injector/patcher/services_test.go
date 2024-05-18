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

package patcher

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServiceAddress(t *testing.T) {
	testCases := []struct {
		svc           Service
		namespace     string
		clusterDomain string
		expect        string
	}{
		{
			svc:           Service{"a", 80},
			namespace:     "b",
			clusterDomain: "cluster.local",
			expect:        "a.b.svc.cluster.local:80",
		},
		{
			svc:           Service{"app", 50001},
			namespace:     "default",
			clusterDomain: "selfdefine.domain",
			expect:        "app.default.svc.selfdefine.domain:50001",
		},
		{
			svc:           ServiceSentry,
			namespace:     "foo",
			clusterDomain: "selfdefine.domain",
			expect:        "dapr-sentry.foo.svc.selfdefine.domain:443",
		},
	}
	for _, tc := range testCases {
		dns := tc.svc.Address(tc.namespace, tc.clusterDomain)
		assert.Equal(t, tc.expect, dns)
	}
}

func TestNewService(t *testing.T) {
	testCases := []struct {
		input    string
		expected Service
		err      bool
	}{
		{
			input:    "a:80",
			expected: Service{"a", 80},
			err:      false,
		},
		{
			input:    "app:50001",
			expected: Service{"app", 50001},
			err:      false,
		},
		{
			input:    "invalid",
			expected: Service{},
			err:      true,
		},
		{
			input:    "name:",
			expected: Service{},
			err:      true,
		},
		{
			input:    ":80",
			expected: Service{},
			err:      true,
		},
	}

	for _, tc := range testCases {
		srv, err := NewService(tc.input)
		if tc.err {
			require.Error(t, err)
		} else {
			require.NoError(t, err)
			assert.Equal(t, tc.expected, srv)
		}
	}
}
