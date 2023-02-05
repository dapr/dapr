//go:build unit
// +build unit

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

package buildinfo

import (
	"strings"
)

// Comma-separated list of features to enable in unit tests
const unitTestFeatures = "Resiliency,ServiceInvocationStreaming"

// Set values for feature flags used in unit tests
func init() {
	if unitTestFeatures == "" {
		featuresSlice = []string{}
	} else {
		featuresSlice = strings.Split(unitTestFeatures, ",")
	}
}
