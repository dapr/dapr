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

	"github.com/stretchr/testify/require"
)

// options contains the options for running a HTTP server in integration tests.
type options struct {
	handler      http.Handler
	handlerFuncs map[string]http.HandlerFunc
	tlsConfig    *tls.Config
	port         *int
}

func WithHandler(handler http.Handler) Option {
	return func(o *options) {
		o.handler = handler
	}
}

func WithPort(port int) Option {
	return func(o *options) {
		o.port = &port
	}
}

func WithHandlerFunc(path string, fn http.HandlerFunc) Option {
	return func(o *options) {
		if o.handlerFuncs == nil {
			o.handlerFuncs = make(map[string]http.HandlerFunc)
		}
		o.handlerFuncs[path] = fn
	}
}

func WithTLS(t *testing.T, cert, key []byte) Option {
	return func(o *options) {
		kp, err := tls.X509KeyPair(cert, key)
		require.NoError(t, err)

		tlsConfig := &tls.Config{
			MinVersion:   tls.VersionTLS12,
			Certificates: []tls.Certificate{kp},
		}

		o.tlsConfig = tlsConfig
	}
}

func WithMTLS(t *testing.T, ca, cert, key []byte) Option {
	return func(o *options) {
		caCertPool := x509.NewCertPool()
		require.True(t, caCertPool.AppendCertsFromPEM(ca))

		cert, err := tls.X509KeyPair(cert, key)
		require.NoError(t, err)

		tlsConfig := &tls.Config{
			MinVersion:   tls.VersionTLS12,
			ClientCAs:    caCertPool,
			ClientAuth:   tls.RequireAndVerifyClientCert,
			Certificates: []tls.Certificate{cert},
		}

		o.tlsConfig = tlsConfig
	}
}
