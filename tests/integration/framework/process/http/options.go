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

package http

import (
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

// options contains the options for running a HTTP server in integration tests.
type options struct {
	handler   http.Handler
	tlsConfig *tls.Config
}

func WithHandler(handler http.Handler) Option {
	return func(o *options) {
		o.handler = handler
	}
}

func WithTLS(cert, key string, t *testing.T) Option {
	return func(o *options) {
		caCertPool := x509.NewCertPool()
		caCertPool.AppendCertsFromPEM([]byte(cert))

		cert, err := tls.X509KeyPair([]byte(cert), []byte(key))
		assert.NoError(t, err)

		tlsConfig := &tls.Config{
			MinVersion:   tls.VersionTLS12,
			ClientCAs:    caCertPool,
			ClientAuth:   tls.RequireAndVerifyClientCert,
			Certificates: []tls.Certificate{cert},
		}

		o.tlsConfig = tlsConfig
	}
}
