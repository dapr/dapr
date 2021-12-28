/*
Copyright 2021 The Dapr Authors
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

package validation

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidationForKubernetes(t *testing.T) {
	t.Run("invalid length", func(t *testing.T) {
		id := ""
		for i := 0; i < 64; i++ {
			id += "a"
		}
		err := ValidateKubernetesAppID(id)
		assert.Error(t, err)
	})

	t.Run("invalid length if suffix -dapr is appended", func(t *testing.T) {
		// service name id+"-dapr" exceeds 63 characters (59 + 5 = 64)
		id := strings.Repeat("a", 59)
		err := ValidateKubernetesAppID(id)
		assert.Error(t, err)
	})

	t.Run("valid id", func(t *testing.T) {
		id := "my-app-id"
		err := ValidateKubernetesAppID(id)
		assert.NoError(t, err)
	})

	t.Run("invalid char: .", func(t *testing.T) {
		id := "my-app-id.app"
		err := ValidateKubernetesAppID(id)
		assert.Error(t, err)
	})

	t.Run("invalid chars space", func(t *testing.T) {
		id := "my-app-id app"
		err := ValidateKubernetesAppID(id)
		assert.Error(t, err)
	})

	t.Run("invalid empty", func(t *testing.T) {
		id := ""
		err := ValidateKubernetesAppID(id)
		assert.Regexp(t, "value for the dapr.io/app-id annotation is empty", err.Error())
	})
}
