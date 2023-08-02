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
)

func TestGetServiceAddress(t *testing.T) {
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
			expect:        "dapr-sentry.foo.svc.selfdefine.domain:80",
		},
	}
	for _, tc := range testCases {
		dns := ServiceAddress(tc.svc, tc.namespace, tc.clusterDomain)
		assert.Equal(t, tc.expect, dns)
	}
}
