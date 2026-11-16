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

package operator

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// AssertListNames asserts that list eventually returns exactly the resources
// named exp, and returns the raw items of that response.
func AssertListNames(t *testing.T, exp []string, list func() ([][]byte, error)) [][]byte {
	t.Helper()

	var items [][]byte
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		var err error
		items, err = list()
		if !assert.NoError(c, err) {
			return
		}

		names := make([]string, 0, len(items))
		for _, b := range items {
			var obj metav1.PartialObjectMetadata
			if !assert.NoError(c, json.Unmarshal(b, &obj)) {
				return
			}
			names = append(names, obj.Name)
		}
		assert.ElementsMatch(c, exp, names)
	}, time.Second*10, time.Millisecond*10)

	return items
}
