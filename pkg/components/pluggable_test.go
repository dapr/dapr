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
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPluggableComponent(t *testing.T) {
	t.Run("get socket path should use /var/run when variable is not set", func(t *testing.T) {
		const fakeName, fakeVersion = "fake", "v1"

		assert.Equal(t, SocketPathForPluggableComponent(fakeName, fakeVersion), "/var/run/dapr-state.fake-v1.sock")
	})
	t.Run("get socket path should use env var when variable is set", func(t *testing.T) {
		const fakeDir = "/tmp"
		t.Setenv(DaprPluggableComponentsSocketFolderEnvVar, fakeDir)
		const fakeName, fakeVersion = "fake", "v1"

		assert.Equal(t, SocketPathForPluggableComponent(fakeName, fakeVersion), fmt.Sprintf("%s/dapr-state.fake-v1.sock", fakeDir))
	})
	t.Run("get socket path should not use version when its empty", func(t *testing.T) {
		const fakeName, fakeVersion = "fake", "v1"

		assert.Equal(t, SocketPathForPluggableComponent(fakeName, fakeVersion), "/var/run/dapr-state.fake.sock")
	})
}
