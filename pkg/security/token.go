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

package security

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/dapr/dapr/pkg/security/consts"
)

const (
	kubeTknPath       = "/var/run/secrets/dapr.io/sentrytoken/token"
	legacyKubeTknPath = "/var/run/secrets/kubernetes.io/serviceaccount/token"
)

var (
	excludedRoutes = []string{"/healthz"}

	// used for testing.
	rootFS = "/"
)

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

// getKubernetesIdentityToken returns the value of the Kubernetes identity
// token.
// If a audience bound token for sentry does not exist, we use the Pods API
// Server token. After all supported Dapr control plane versions support
// audience bound tokens, we can remove this function and use the audience
// bound token.
func getKubernetesIdentityToken() (string, error) {
	b, err := os.ReadFile(filepath.Join(rootFS, kubeTknPath))
	if os.IsNotExist(err) {
		log.Warn("⚠️ daprd is initializing using the legacy service account token with access to Kubernetes APIs, which is discouraged. This usually happens when daprd is running against an older version of the Dapr control plane.")
		// Attempt to use the legacy token if that exists
		b, err = os.ReadFile(filepath.Join(rootFS, legacyKubeTknPath))
	}
	if err != nil {
		return "", err
	}

	return string(b), err
}
