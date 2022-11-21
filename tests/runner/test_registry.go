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
	"os"
)

const (
	// defaultImageRegistry is the registry used by test apps by default.
	defaultImageRegistry = "docker.io/dapriotest"
	// defaultImageTag is the default image used for test apps.
	defaultImageTag = "latest"
)

// getTestImageRegistry get the test image registry from the env var or uses the default value if not present or empty.
func getTestImageRegistry() string {
	reg := os.Getenv("DAPR_TEST_REGISTRY")
	if reg == "" {
		return defaultImageRegistry
	}
	return reg
}

// getTestImageSecret get the test image secret from the env var or uses the default value if not present or empty.
func getTestImageSecret() string {
	secret := os.Getenv("DAPR_TEST_REGISTRY_SECRET")
	if secret == "" {
		return ""
	}
	return secret
}

// getTestImageTag get the test image Tag from the env var or uses the default value if not present or empty.
func getTestImageTag() string {
	tag := os.Getenv("DAPR_TEST_TAG")
	if tag == "" {
		return defaultImageTag
	}
	return tag
}

// BuildTestImage name uses the default registry and tag to build a image name for the given test app.
func BuildTestImageName(appName string) string {
	return fmt.Sprintf("%s/%s:%s", getTestImageRegistry(), appName, getTestImageTag())
}
