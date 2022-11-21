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

package security

import (
	"os"
	"strings"

	"github.com/dapr/dapr/pkg/runtime/security/consts"
)

var excludedRoutes = []string{"/healthz"}

// GetAPIToken returns the value of the api token from an environment variable.
func GetAPIToken() string {
	return os.Getenv(consts.APITokenEnvVar)
}

// GetAppToken returns the value of the app api token from an environment variable.
func GetAppToken() string {
	return os.Getenv(consts.AppAPITokenEnvVar)
}

// ExcludedRoute returns whether a given route should be excluded from a token check.
func ExcludedRoute(route string) bool {
	for _, r := range excludedRoutes {
		if strings.Contains(route, r) {
			return true
		}
	}
	return false
}
