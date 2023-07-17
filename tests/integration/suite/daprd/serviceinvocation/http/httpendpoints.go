/*
Copyright 2023 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implieh.
See the License for the specific language governing permissions and
limitations under the License.
*/

package http

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	procdaprd "github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(httpendpoints))
}

type httpendpoints struct {
	daprd1  *procdaprd.Daprd
	appPort int
}

func (h *httpendpoints) Setup(t *testing.T) []framework.Option {
	newHTTPServer := func() *prochttp.HTTP {
		handler := http.NewServeMux()

		handler.HandleFunc("/hello", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("ok"))
		})

		return prochttp.New(t, prochttp.WithHandler(handler))
	}

	newHTTPServerTLS := func() *prochttp.HTTP {
		handler := http.NewServeMux()

		handler.HandleFunc("/hello", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("ok"))
		})

		return prochttp.New(t, prochttp.WithHandler(handler), prochttp.WithTLS("-----BEGIN CERTIFICATE-----\nMIID8zCCAtugAwIBAgIUSj2XqIQo/xmVVuw1+nmIcdmr48IwDQYJKoZIhvcNAQEL\nBQAwfjELMAkGA1UEBhMCVVMxEzARBgNVBAgMCkNhbGlmb3JuaWExFjAUBgNVBAcM\nDU1vdW50YWluIFZpZXcxGjAYBgNVBAoMEVlvdXIgT3JnYW5pemF0aW9uMRIwEAYD\nVQQLDAlZb3VyIFVuaXQxEjAQBgNVBAMMCWxvY2FsaG9zdDAeFw0yMzA3MTQyMTQ1\nNDRaFw0zMzA3MTEyMTQ1NDRaMH4xCzAJBgNVBAYTAlVTMRMwEQYDVQQIDApDYWxp\nZm9ybmlhMRYwFAYDVQQHDA1Nb3VudGFpbiBWaWV3MRowGAYDVQQKDBFZb3VyIE9y\nZ2FuaXphdGlvbjESMBAGA1UECwwJWW91ciBVbml0MRIwEAYDVQQDDAlsb2NhbGhv\nc3QwggEiMA0GCSqGSIb3DQEBAQUAA4IBDwAwggEKAoIBAQCmWsrT0w4s0g7Ld1g/\nIa0qM2v6tXG8L/150kwM7V/wRSVCx+Tmv+aAMAJzJtXwMAj9jHt+sH5vOm6MJenJ\n0sTyUcCC8FhLa0VWxbajZOn8wKaUuZzLj3Uye/q4z0l6rtu35pjZCjDKphyycKoG\nNG2MwcGNNBUZP0W6Idw0PZaNstnXnTHIWE6axOA9bzB5iKtLTVkQDkuEJWFLfOD6\nZN4ISA+7HvbMgjREeIuNJqNI/746Egza5a4ZPh3KvyIPEdH2SdDy+9obCsdAKe65\nSEIPy1n4zRy3/7K1bVTu18JoWsNtE+5GDWVgFU3AVZ9ic6Jt0N0WNaRuUiiVpvKE\nLzyBAgMBAAGjaTBnMB0GA1UdDgQWBBQj02E1/fJHbm8GnOMNxfSIRI2cqjAfBgNV\nHSMEGDAWgBQj02E1/fJHbm8GnOMNxfSIRI2cqjAPBgNVHRMBAf8EBTADAQH/MBQG\nA1UdEQQNMAuCCWxvY2FsaG9zdDANBgkqhkiG9w0BAQsFAAOCAQEANRqDGWWdaXwg\nhkOz/Y9gkil6WYQ4d7muAAqiEZBqg+yAisGrv+MPeyjsrq1WPbIgcdlSjArG7zGV\n/5SPZwsuTeivdFs0DHbcvoKA8hqN2n4OmtjzdommBWA2618fzByOufECWRmUBxH3\nqmkmQ+aj4wKXm2U22OFxmWPTJbtwi8MDppl+G3AH4VpRi6qhSmLBSAO3AEBr7UJN\nIRHeUzBsFDLE++a3WYRnI0kA9og9F+zbnD+kNMxxTfmZqB6T9iFHIzkxZw7RELey\nz2PxQioHRQtp0rVZWnDt6+qSfrqftZgszrM+06n1MHTPBMusBfG/uyEAc4EAStCC\njXHuph+Ctg==\n-----END CERTIFICATE-----\n", "-----BEGIN PRIVATE KEY-----\nMIIEvAIBADANBgkqhkiG9w0BAQEFAASCBKYwggSiAgEAAoIBAQCmWsrT0w4s0g7L\nd1g/Ia0qM2v6tXG8L/150kwM7V/wRSVCx+Tmv+aAMAJzJtXwMAj9jHt+sH5vOm6M\nJenJ0sTyUcCC8FhLa0VWxbajZOn8wKaUuZzLj3Uye/q4z0l6rtu35pjZCjDKphyy\ncKoGNG2MwcGNNBUZP0W6Idw0PZaNstnXnTHIWE6axOA9bzB5iKtLTVkQDkuEJWFL\nfOD6ZN4ISA+7HvbMgjREeIuNJqNI/746Egza5a4ZPh3KvyIPEdH2SdDy+9obCsdA\nKe65SEIPy1n4zRy3/7K1bVTu18JoWsNtE+5GDWVgFU3AVZ9ic6Jt0N0WNaRuUiiV\npvKELzyBAgMBAAECggEABXHZS5+PyjXB2DT6xW4zvbrbIOSJaXBkqnUQmie2ySVq\nN8pVGpxTTgTEP8KYo/jegnXzoMzkBn3yGlIvWbS1T30PgPme2jETnuhvtt9ZrTUc\n/qcok50JZ/KY3S2jqQlKFbXNcOUdfbR8IfcACZ3zq/S3ggifXCku/g2XqHoPkGmp\nokEjXEJLX9f/ABgik3a5aaSCMYfCU9PzDNCM7vjiWxUvO0V5kjqYae+SpMQCvvTb\n1JEMEsawCEeGI9BBnj4o1x7xGDpFn4Yt6MznaHivANMHqqNKaH3LK6rFZGdnj7d6\nutpJ22QZUBYhZ1+Hz+WNUQD+z+O2NGfMEo0PZvb1MQKBgQDZ6TlehduU0JcgKUI+\nFSq5wAk+Eil158arCU3CWnn69VLUu5lAjkU4dXjmkg/c9nxRpg5kuWr8FGMmzTpx\nWlWZj1b72iTQuWX0fphNif3mljeDNLl5z0fGegHjH+KkGb9y6t06oKrsEZbxn4y0\nzOLfl1t85tPAMP6RuvfawpjBjQKBgQDDbpifamPx/GE1JdKj6M6At42kmDczd5P0\nlgN39BRC6fUS19CPurKVWF501+w52rxg1NWhtQVmW1DUdWpDUwQtRPXo43Ku8MPN\nRMD8Uj+PNgXgWnWdcfGbniB8xHqsE8N7MgKU5IZQOqEIDCVE4RGheTYGNgDZ16Rz\nILmZ14E3xQKBgHtI3fJCXSbmlHnXneit5QxOP2xkrhxM0zN1Ag9RTO3U2dYNhPjn\nBPaaT5pzTJJAybkP79jApmyTxDzxo3z6FK/aTuYSVv3Xxnz7GoPT7FgG6MVMkRr/\nUKZT5LlxErKw9oW3pw5CVDFXCkUNdXfc6waBBXu2xFpZ3czpMM0Nh4sJAoGAEpGo\nmMUQGAcF6XndiMtvC5XlNHVuEUrUWRID5FrhrfXy3kZ5P57apwwNdYaqoFijO4Qd\nhE7h43bbuEQrw5fYtsBtqSIrXGnuAMv+ljruZRoZ9tZBhKM19LZSmehFS6JZGZSH\n4EPSaz8W29/jjqbf+Pq+YlqxPAGcU4ARgoeSdI0CgYACG9WZMbri9Eas1qA3vP86\nVW917u7CKt3O6Gsr4F5BNIH3Qx9BReLB9cDvhyko/JAln0MiNq2nExeuFlhEqVsx\nmn681Xm6yPht1PNeTkrRroXJIVzbBOldFW7evX/g/izeiXH6YhfGKD6B8dBfUftx\nNcs6FFLydFcIdIxebYjYnQ==\n-----END PRIVATE KEY-----\n"))
	}

	srv1 := newHTTPServer()
	srv2 := newHTTPServerTLS()

	h.daprd1 = procdaprd.New(t, procdaprd.WithResourceFiles(fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: HTTPEndpoint
metadata:
  name: mywebsite 
spec:
  version: v1alpha1
  baseUrl: http://localhost:%v
`, srv1.Port()), fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: HTTPEndpoint
metadata:
  name: mywebsitetls
spec:
  version: v1alpha1
  baseUrl: http://localhost:%v
  tlsRootCA:
    value: "-----BEGIN CERTIFICATE-----\nMIID8zCCAtugAwIBAgIUSj2XqIQo/xmVVuw1+nmIcdmr48IwDQYJKoZIhvcNAQEL\nBQAwfjELMAkGA1UEBhMCVVMxEzARBgNVBAgMCkNhbGlmb3JuaWExFjAUBgNVBAcM\nDU1vdW50YWluIFZpZXcxGjAYBgNVBAoMEVlvdXIgT3JnYW5pemF0aW9uMRIwEAYD\nVQQLDAlZb3VyIFVuaXQxEjAQBgNVBAMMCWxvY2FsaG9zdDAeFw0yMzA3MTQyMTQ1\nNDRaFw0zMzA3MTEyMTQ1NDRaMH4xCzAJBgNVBAYTAlVTMRMwEQYDVQQIDApDYWxp\nZm9ybmlhMRYwFAYDVQQHDA1Nb3VudGFpbiBWaWV3MRowGAYDVQQKDBFZb3VyIE9y\nZ2FuaXphdGlvbjESMBAGA1UECwwJWW91ciBVbml0MRIwEAYDVQQDDAlsb2NhbGhv\nc3QwggEiMA0GCSqGSIb3DQEBAQUAA4IBDwAwggEKAoIBAQCmWsrT0w4s0g7Ld1g/\nIa0qM2v6tXG8L/150kwM7V/wRSVCx+Tmv+aAMAJzJtXwMAj9jHt+sH5vOm6MJenJ\n0sTyUcCC8FhLa0VWxbajZOn8wKaUuZzLj3Uye/q4z0l6rtu35pjZCjDKphyycKoG\nNG2MwcGNNBUZP0W6Idw0PZaNstnXnTHIWE6axOA9bzB5iKtLTVkQDkuEJWFLfOD6\nZN4ISA+7HvbMgjREeIuNJqNI/746Egza5a4ZPh3KvyIPEdH2SdDy+9obCsdAKe65\nSEIPy1n4zRy3/7K1bVTu18JoWsNtE+5GDWVgFU3AVZ9ic6Jt0N0WNaRuUiiVpvKE\nLzyBAgMBAAGjaTBnMB0GA1UdDgQWBBQj02E1/fJHbm8GnOMNxfSIRI2cqjAfBgNV\nHSMEGDAWgBQj02E1/fJHbm8GnOMNxfSIRI2cqjAPBgNVHRMBAf8EBTADAQH/MBQG\nA1UdEQQNMAuCCWxvY2FsaG9zdDANBgkqhkiG9w0BAQsFAAOCAQEANRqDGWWdaXwg\nhkOz/Y9gkil6WYQ4d7muAAqiEZBqg+yAisGrv+MPeyjsrq1WPbIgcdlSjArG7zGV\n/5SPZwsuTeivdFs0DHbcvoKA8hqN2n4OmtjzdommBWA2618fzByOufECWRmUBxH3\nqmkmQ+aj4wKXm2U22OFxmWPTJbtwi8MDppl+G3AH4VpRi6qhSmLBSAO3AEBr7UJN\nIRHeUzBsFDLE++a3WYRnI0kA9og9F+zbnD+kNMxxTfmZqB6T9iFHIzkxZw7RELey\nz2PxQioHRQtp0rVZWnDt6+qSfrqftZgszrM+06n1MHTPBMusBfG/uyEAc4EAStCC\njXHuph+Ctg==\n-----END CERTIFICATE-----\n"
  tlsClientCert:
    value: "-----BEGIN CERTIFICATE-----\nMIID8zCCAtugAwIBAgIUSj2XqIQo/xmVVuw1+nmIcdmr48IwDQYJKoZIhvcNAQEL\nBQAwfjELMAkGA1UEBhMCVVMxEzARBgNVBAgMCkNhbGlmb3JuaWExFjAUBgNVBAcM\nDU1vdW50YWluIFZpZXcxGjAYBgNVBAoMEVlvdXIgT3JnYW5pemF0aW9uMRIwEAYD\nVQQLDAlZb3VyIFVuaXQxEjAQBgNVBAMMCWxvY2FsaG9zdDAeFw0yMzA3MTQyMTQ1\nNDRaFw0zMzA3MTEyMTQ1NDRaMH4xCzAJBgNVBAYTAlVTMRMwEQYDVQQIDApDYWxp\nZm9ybmlhMRYwFAYDVQQHDA1Nb3VudGFpbiBWaWV3MRowGAYDVQQKDBFZb3VyIE9y\nZ2FuaXphdGlvbjESMBAGA1UECwwJWW91ciBVbml0MRIwEAYDVQQDDAlsb2NhbGhv\nc3QwggEiMA0GCSqGSIb3DQEBAQUAA4IBDwAwggEKAoIBAQCmWsrT0w4s0g7Ld1g/\nIa0qM2v6tXG8L/150kwM7V/wRSVCx+Tmv+aAMAJzJtXwMAj9jHt+sH5vOm6MJenJ\n0sTyUcCC8FhLa0VWxbajZOn8wKaUuZzLj3Uye/q4z0l6rtu35pjZCjDKphyycKoG\nNG2MwcGNNBUZP0W6Idw0PZaNstnXnTHIWE6axOA9bzB5iKtLTVkQDkuEJWFLfOD6\nZN4ISA+7HvbMgjREeIuNJqNI/746Egza5a4ZPh3KvyIPEdH2SdDy+9obCsdAKe65\nSEIPy1n4zRy3/7K1bVTu18JoWsNtE+5GDWVgFU3AVZ9ic6Jt0N0WNaRuUiiVpvKE\nLzyBAgMBAAGjaTBnMB0GA1UdDgQWBBQj02E1/fJHbm8GnOMNxfSIRI2cqjAfBgNV\nHSMEGDAWgBQj02E1/fJHbm8GnOMNxfSIRI2cqjAPBgNVHRMBAf8EBTADAQH/MBQG\nA1UdEQQNMAuCCWxvY2FsaG9zdDANBgkqhkiG9w0BAQsFAAOCAQEANRqDGWWdaXwg\nhkOz/Y9gkil6WYQ4d7muAAqiEZBqg+yAisGrv+MPeyjsrq1WPbIgcdlSjArG7zGV\n/5SPZwsuTeivdFs0DHbcvoKA8hqN2n4OmtjzdommBWA2618fzByOufECWRmUBxH3\nqmkmQ+aj4wKXm2U22OFxmWPTJbtwi8MDppl+G3AH4VpRi6qhSmLBSAO3AEBr7UJN\nIRHeUzBsFDLE++a3WYRnI0kA9og9F+zbnD+kNMxxTfmZqB6T9iFHIzkxZw7RELey\nz2PxQioHRQtp0rVZWnDt6+qSfrqftZgszrM+06n1MHTPBMusBfG/uyEAc4EAStCC\njXHuph+Ctg==\n-----END CERTIFICATE-----\n"
  tlsClientKey:
    value: "-----BEGIN PRIVATE KEY-----\nMIIEvAIBADANBgkqhkiG9w0BAQEFAASCBKYwggSiAgEAAoIBAQCmWsrT0w4s0g7L\nd1g/Ia0qM2v6tXG8L/150kwM7V/wRSVCx+Tmv+aAMAJzJtXwMAj9jHt+sH5vOm6M\nJenJ0sTyUcCC8FhLa0VWxbajZOn8wKaUuZzLj3Uye/q4z0l6rtu35pjZCjDKphyy\ncKoGNG2MwcGNNBUZP0W6Idw0PZaNstnXnTHIWE6axOA9bzB5iKtLTVkQDkuEJWFL\nfOD6ZN4ISA+7HvbMgjREeIuNJqNI/746Egza5a4ZPh3KvyIPEdH2SdDy+9obCsdA\nKe65SEIPy1n4zRy3/7K1bVTu18JoWsNtE+5GDWVgFU3AVZ9ic6Jt0N0WNaRuUiiV\npvKELzyBAgMBAAECggEABXHZS5+PyjXB2DT6xW4zvbrbIOSJaXBkqnUQmie2ySVq\nN8pVGpxTTgTEP8KYo/jegnXzoMzkBn3yGlIvWbS1T30PgPme2jETnuhvtt9ZrTUc\n/qcok50JZ/KY3S2jqQlKFbXNcOUdfbR8IfcACZ3zq/S3ggifXCku/g2XqHoPkGmp\nokEjXEJLX9f/ABgik3a5aaSCMYfCU9PzDNCM7vjiWxUvO0V5kjqYae+SpMQCvvTb\n1JEMEsawCEeGI9BBnj4o1x7xGDpFn4Yt6MznaHivANMHqqNKaH3LK6rFZGdnj7d6\nutpJ22QZUBYhZ1+Hz+WNUQD+z+O2NGfMEo0PZvb1MQKBgQDZ6TlehduU0JcgKUI+\nFSq5wAk+Eil158arCU3CWnn69VLUu5lAjkU4dXjmkg/c9nxRpg5kuWr8FGMmzTpx\nWlWZj1b72iTQuWX0fphNif3mljeDNLl5z0fGegHjH+KkGb9y6t06oKrsEZbxn4y0\nzOLfl1t85tPAMP6RuvfawpjBjQKBgQDDbpifamPx/GE1JdKj6M6At42kmDczd5P0\nlgN39BRC6fUS19CPurKVWF501+w52rxg1NWhtQVmW1DUdWpDUwQtRPXo43Ku8MPN\nRMD8Uj+PNgXgWnWdcfGbniB8xHqsE8N7MgKU5IZQOqEIDCVE4RGheTYGNgDZ16Rz\nILmZ14E3xQKBgHtI3fJCXSbmlHnXneit5QxOP2xkrhxM0zN1Ag9RTO3U2dYNhPjn\nBPaaT5pzTJJAybkP79jApmyTxDzxo3z6FK/aTuYSVv3Xxnz7GoPT7FgG6MVMkRr/\nUKZT5LlxErKw9oW3pw5CVDFXCkUNdXfc6waBBXu2xFpZ3czpMM0Nh4sJAoGAEpGo\nmMUQGAcF6XndiMtvC5XlNHVuEUrUWRID5FrhrfXy3kZ5P57apwwNdYaqoFijO4Qd\nhE7h43bbuEQrw5fYtsBtqSIrXGnuAMv+ljruZRoZ9tZBhKM19LZSmehFS6JZGZSH\n4EPSaz8W29/jjqbf+Pq+YlqxPAGcU4ARgoeSdI0CgYACG9WZMbri9Eas1qA3vP86\nVW917u7CKt3O6Gsr4F5BNIH3Qx9BReLB9cDvhyko/JAln0MiNq2nExeuFlhEqVsx\nmn681Xm6yPht1PNeTkrRroXJIVzbBOldFW7evX/g/izeiXH6YhfGKD6B8dBfUftx\nNcs6FFLydFcIdIxebYjYnQ==\n-----END PRIVATE KEY-----\n"
`, srv2.Port())))
	h.appPort = srv1.Port()

	return []framework.Option{
		framework.WithProcesses(srv1, srv2, h.daprd1),
	}
}

func (h *httpendpoints) Run(t *testing.T, ctx context.Context) {
	h.daprd1.WaitUntilRunning(t, ctx)

	t.Run("invoke http endpoint", func(t *testing.T) {
		doReq := func(method, url string, headers map[string]string) (int, string) {
			req, err := http.NewRequestWithContext(ctx, method, url, nil)
			require.NoError(t, err)
			for k, v := range headers {
				req.Header.Set(k, v)
			}
			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.NoError(t, resp.Body.Close())
			return resp.StatusCode, string(body)
		}

		for i, ts := range []struct {
			url     string
			headers map[string]string
		}{
			{url: fmt.Sprintf("http://localhost:%d/v1.0/invoke/http://localhost:%d/method/hello", h.daprd1.HTTPPort(), h.appPort)},
			{url: fmt.Sprintf("http://localhost:%d/v1.0/invoke/mywebsite/method/hello", h.daprd1.HTTPPort())},
		} {
			t.Run(fmt.Sprintf("url %d", i), func(t *testing.T) {
				status, body := doReq(http.MethodGet, ts.url, ts.headers)
				assert.Equal(t, http.StatusOK, status)
				assert.Equal(t, "ok", body)
			})
		}
	})

	t.Run("invoke TLS http endpoint", func(t *testing.T) {
		doReq := func(method, url string, headers map[string]string) (int, string) {
			req, err := http.NewRequestWithContext(ctx, method, url, nil)
			require.NoError(t, err)
			for k, v := range headers {
				req.Header.Set(k, v)
			}
			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.NoError(t, resp.Body.Close())
			return resp.StatusCode, string(body)
		}

		for i, ts := range []struct {
			url     string
			headers map[string]string
		}{
			{url: fmt.Sprintf("http://localhost:%d/v1.0/invoke/mywebsitetls/method/hello", h.daprd1.HTTPPort())},
		} {
			t.Run(fmt.Sprintf("url %d", i), func(t *testing.T) {
				status, body := doReq(http.MethodGet, ts.url, ts.headers)
				assert.Equal(t, http.StatusOK, status)
				assert.Equal(t, "ok", body)
			})
		}
	})
}
