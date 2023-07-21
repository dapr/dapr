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
	"path/filepath"

	"github.com/dapr/dapr/pkg/security/consts"
)

const (
	kubeTknPath = "/var/run/secrets/dapr.io/sentrytoken/token"
)

// used for testing.
var rootFS = "/"

// GetAPIToken returns the value of the api token from an environment variable.
func GetAPIToken() string {
	return os.Getenv(consts.APITokenEnvVar)
}

// GetAppToken returns the value of the app api token from an environment variable.
func GetAppToken() string {
	return os.Getenv(consts.AppAPITokenEnvVar)
}

// getKubernetesIdentityToken returns the value of the Kubernetes identity
// token.
func getKubernetesIdentityToken() (string, error) {
	b, err := os.ReadFile(filepath.Join(rootFS, kubeTknPath))
	if err != nil {
		return "", err
	}

	return string(b), err
}
