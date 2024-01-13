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
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	procsentry "github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(httpsCA))
}

// httpsCA tests Sentry with the JWKS validator fetching from a HTTPS URL with a custom CA
type httpsCA struct {
	shared
}

func (m *httpsCA) Setup(t *testing.T) []framework.Option {
	// Generate a CA and key pair for the JWKS server
	caCert, caKey := generateCACertificate(t)
	serverCert, serverKey := generateTLSCertificates(t, caCert, caKey, "localhost")

	jwksServer := prochttp.New(t,
		prochttp.WithHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, `{"keys":[`+string(jwtSigningKeyPubJSON)+`]}`)
		})),
		prochttp.WithTLS(t, serverCert, serverKey),
	)

	caCertJSON, err := json.Marshal(string(caCert))
	require.NoError(t, err)

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
          source: "https://localhost:` + strconv.Itoa(jwksServer.Port()) + `/"
          caCertificate: ` + string(caCertJSON) + `
`

	m.proc = procsentry.New(t, procsentry.WithConfiguration(jwksConfig))

	return []framework.Option{
		framework.WithProcesses(jwksServer, m.proc),
	}
}
