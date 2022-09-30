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

package runner

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBuildImageName(t *testing.T) {
	t.Run("build image name should use default values when none was set", func(t *testing.T) {
		t.Setenv("DAPR_TEST_REGISTRY", "")
		t.Setenv("DAPR_TEST_TAG", "")

		const fakeApp = "fake"
		image := BuildTestImageName(fakeApp)
		assert.Equal(t, image, fmt.Sprintf("%s/%s:%s", defaultImageRegistry, fakeApp, defaultImageTag))
	})
	t.Run("build image name should use test image registry when set", func(t *testing.T) {
		const fakeRegistry = "fake-registry"
		t.Setenv("DAPR_TEST_REGISTRY", fakeRegistry)
		t.Setenv("DAPR_TEST_TAG", "")

		const fakeApp = "fake"
		image := BuildTestImageName(fakeApp)
		assert.Equal(t, image, fmt.Sprintf("%s/%s:%s", fakeRegistry, fakeApp, defaultImageTag))
	})
	t.Run("build image name should use test tag when set", func(t *testing.T) {
		const fakeTag = "fake-tag"
		t.Setenv("DAPR_TEST_REGISTRY", "")
		t.Setenv("DAPR_TEST_TAG", fakeTag)

		const fakeApp = "fake"
		image := BuildTestImageName(fakeApp)
		assert.Equal(t, image, fmt.Sprintf("%s/%s:%s", defaultImageRegistry, fakeApp, fakeTag))
	})
}
