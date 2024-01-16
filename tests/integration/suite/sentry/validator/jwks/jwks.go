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

package jwks

import (
	"testing"

	"github.com/dapr/dapr/tests/integration/framework"
	procsentry "github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(jwks))
}

// jwks tests Sentry with the JWKS validator.
type jwks struct {
	shared
}

func (m *jwks) Setup(t *testing.T) []framework.Option {
	jwksConfig := `
kind: Configuration
apiVersion: dapr.io/v1alpha1
metadata:
  name: sentryconfig
spec:
  mtls:
    enabled: true
    tokenValidators:
      - name: jwks
        options:
          minRefreshInterval: 2m
          requestTimeout: 1m
          source: |
            {"keys":[` + string(jwtSigningKeyPubJSON) + `]}
`

	m.proc = procsentry.New(t, procsentry.WithConfiguration(jwksConfig))

	return []framework.Option{
		framework.WithProcesses(m.proc),
	}
}
