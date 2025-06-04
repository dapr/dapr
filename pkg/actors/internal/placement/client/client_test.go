/*
Copyright 2024 The Dapr Authors
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

package client

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestK8sMode(t *testing.T) {
	testCases := []struct {
		addr    []string
		k8sMode bool
	}{
		{
			addr:    []string{"placement1:50005", "placement2:50005", "placement3:50005"},
			k8sMode: false,
		}, {
			addr:    []string{"192.168.0.100:50005", "192.168.0.101:50005", "192.168.0.102:50005"},
			k8sMode: false,
		}, {
			addr:    []string{"placement1:50005"},
			k8sMode: true,
		}, {
			addr:    []string{"192.168.0.100:50005"},
			k8sMode: false,
		},
	}
	for _, tc := range testCases {
		assert.EqualValues(t, tc.k8sMode, isKubernetesMode(tc.addr))
	}
}
