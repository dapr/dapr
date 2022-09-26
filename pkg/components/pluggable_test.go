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

package components

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPluggableMethods(t *testing.T) {
	const fakeName, fakeType, fakeVersion = "fakeName", "fakeType", "v1"
	t.Run("full name should return component name with its version when specified", func(t *testing.T) {
		pc := Pluggable{
			Name:    fakeName,
			Type:    fakeType,
			Version: fakeVersion,
		}
		assert.Equal(t, pc.FullName(), "fakeName/v1")
	})

	t.Run("full name should return only component name when version not specified", func(t *testing.T) {
		pc := Pluggable{
			Name: fakeName,
			Type: fakeType,
		}
		assert.Equal(t, pc.FullName(), fakeName)
	})
}
