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

		privKey := "-----BEGIN PRIVATE KEY-----\nMIIEvAIBADANBgkqhkiG9w0BAQEFAASCBKYwggSiAgEAAoIBAQCJFS3cMM+6e3KZ\nPriLhkvzU6TE0TWGAZz08Z8I4GOTms/wsGBFr3mZoFNsrkZg0kOo+0PoC1WlXcPZ\n21zhDQJn/y2sUGfRypi41yGjXd/rLRjyIfsz1GsCj1dnVQDCGhJP0ZwH7DdnhVE+\nww0FuUcmmlr0EaAfsupkQhlE9QKyfrZE4WPF/HumC9vLbvpaNw/95cumctUilVMx\nK04YpyLtSXShmRvXjVUej/zS6HrDOFbu05lAO+E6BH+p747+n08EV75FOj/5iFYM\npF0+2Nt0QXsqJW4od9AlHaCsL2wf8rubpLrzbyKmbDqQpxKpB9749ReEfIVBKymK\nCR7JwstTAgMBAAECggEACdHywUGH6AqG456AIZKjJzvDOSGUQtTOayenURSjXYQW\nHbPCOcu/xiHGSCoAkFgPu8UVkMPFxL0VANV7mlhQZ1B686CmR1D1pCwufzcENE3n\nPxVFxb6TqeZzMvZTwb6UuD4XqOOi9m9bqhzWoSXW3deUk25zI5eGvgbpgNdHya86\nRLZgEDGSnnAMZi7FANXR+gtvHQmXYP9CM/1rivYQI/MXd4jbZEVWeNZEc1u1YqVC\nM7ddN063KmE/LQewF2dHhw5dyhh1udgpoXuOFhmkbwBPLn6eVJ/GdKRG0+LDfOmW\nFx6QN7HV0gYISdVIUQBOsEPSh2LEurKDJyqzFLUoiQKBgQC+B08abD4D+3JQTmPV\nZuHusL3wizZxx4YQ1SNULMOv+i7oP22ERXsCCrnRGn+JPM/jc2HV0I3Z67BS9a2M\nUjRVZ54rN5DTWP+XAN1rCcDkxrp0CZ9pL4UZwcAZyanIIYCc0RcTHM//2WblLtHg\n60OJ5lMTlqc2LhQquwIM/5iAKwKBgQC4rFZYXoj6AYkdEyQSWcqOrtH72h5GZ9Rw\nmETRk8GeMI2gD6t92DWZmZcC+ycUGDxBX1A7fQ+imw31KUz0ciAX+aDWEgUBOgS5\n50KvRAo9JcHxyj5qXPlXpCHItaGjd2A1KexvKRxN4GngEouQcOTSF69pyP4P837v\nFfVclHgleQKBgHa8rmq+M8ndNcKEGNFhJc81VJmXAv/5QgYGh7qy9dthoimwaEf7\n+i5+cTj9K6+e9e8TS5CEbf46zMQpirVhMB4lTqmGYNIOjDXYboHRaFwK6LpUwOzC\nqpI7hEMYxSOt+2UBKT/iAv3E5AxUQwQdPOhPqJ1Wx0iNZkCE9XUVyD5dAoGADY6B\nLC2MaqXwNdUw4bP7vauxuIZTkKGQo89ZxfTN0toHm4dq9GsJzEPNJSfgv4Xj7nyb\nvDI7EpnFVYj5oDw8hOYLW4upGGT08dy7NXiOM9zwtto86Lv4hemDnWNQAVsDEgQI\n2kQvUrw1qYBTBIB1G8MnWmGp3OvoFo8LGwe/JikCgYA9RgIuXNkcYMPtRuOzX/ne\nCxP3enIwGgTfMZl7i4wprbv11FwoihhhkPjnY/tQikvmw6zTkqPrnALdDia15kmt\n0YpGrkkFOQ1RzPP4brOr17mcA2RQqfzWxq/trXiJVVIch7c4EBliUCH+qr7WMGUK\nb0K2SICFbq1Z4B4X9/MqGw==\n-----END PRIVATE KEY-----\n"
		cert := "-----BEGIN CERTIFICATE-----\nMIIDiTCCAnGgAwIBAgIUUy9I/W0Fp+ie3Wyx9IPTW/vXPLswDQYJKoZIhvcNAQEL\nBQAwUzELMAkGA1UEBhMCQ04xCzAJBgNVBAgMAkdEMQswCQYDVQQHDAJTWjETMBEG\nA1UECgwKQWNtZSwgSW5jLjEVMBMGA1UEAwwMQWNtZSBSb290IENBMB4XDTIzMDcx\nOTIyNTU1OFoXDTI0MDcxODIyNTU1OFowUDELMAkGA1UEBhMCQ04xCzAJBgNVBAgM\nAkdEMQswCQYDVQQHDAJTWjETMBEGA1UECgwKQWNtZSwgSW5jLjESMBAGA1UEAwwJ\nbG9jYWxob3N0MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEAiRUt3DDP\nuntymT64i4ZL81OkxNE1hgGc9PGfCOBjk5rP8LBgRa95maBTbK5GYNJDqPtD6AtV\npV3D2dtc4Q0CZ/8trFBn0cqYuNcho13f6y0Y8iH7M9RrAo9XZ1UAwhoST9GcB+w3\nZ4VRPsMNBblHJppa9BGgH7LqZEIZRPUCsn62ROFjxfx7pgvby276WjcP/eXLpnLV\nIpVTMStOGKci7Ul0oZkb141VHo/80uh6wzhW7tOZQDvhOgR/qe+O/p9PBFe+RTo/\n+YhWDKRdPtjbdEF7KiVuKHfQJR2grC9sH/K7m6S6828ipmw6kKcSqQfe+PUXhHyF\nQSspigkeycLLUwIDAQABo1gwVjAUBgNVHREEDTALgglsb2NhbGhvc3QwHQYDVR0O\nBBYEFJ0CtzmDzMw7CAbpubBTlzX52jePMB8GA1UdIwQYMBaAFFtgL/fp9MtNy+bY\nhCKSx4ADHDHnMA0GCSqGSIb3DQEBCwUAA4IBAQAOor9yXrGLItkUVX7PXUhnS7Cu\nA8W7tlAtLCk+MW3b3qGeoy19mr/q+olD8T1lfWG2K8DSHKwxfkGLGSxsz4/9iUhF\nujXDJrGA7znVWF3kElh/tlsAOiKZ/tE9XXiuK1hghcV/s0d2FgANZh5xmkudZa1m\nnqRJe36+bxWfL490u3L9lO3gQE7zlcvR5TNeRcWe7TyHGDkU3dYIzSmvX32jtKC3\nVUWU9Z8cmIy2mMOomdxlzMoSdOCw19hecgSidZArXTNo+uOgKFnYDp+RxC0DwK1f\n0tW1Zhudkuj3uz+5KdXzAQ8xGse5dNl/n2KMZPh/EqH/3EwGT2tmrkjB+ikx\n-----END CERTIFICATE-----\n"

		return prochttp.New(t, prochttp.WithHandler(handler), prochttp.WithTLS(cert, privKey, t))
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
  baseUrl: http://localhost:%d
`, srv1.Port()), fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: HTTPEndpoint
metadata:
  name: mywebsitetls
spec:
  version: v1alpha1
  baseUrl: http://localhost:%d
  tlsRootCA:
    value: "-----BEGIN CERTIFICATE-----\nMIIDhzCCAm+gAwIBAgIUfC71D5CDcoowPmzDCaOdgBzbfmgwDQYJKoZIhvcNAQEL\nBQAwUzELMAkGA1UEBhMCQ04xCzAJBgNVBAgMAkdEMQswCQYDVQQHDAJTWjETMBEG\nA1UECgwKQWNtZSwgSW5jLjEVMBMGA1UEAwwMQWNtZSBSb290IENBMB4XDTIzMDcx\nOTIyNTU1MFoXDTI0MDcxODIyNTU1MFowUzELMAkGA1UEBhMCQ04xCzAJBgNVBAgM\nAkdEMQswCQYDVQQHDAJTWjETMBEGA1UECgwKQWNtZSwgSW5jLjEVMBMGA1UEAwwM\nQWNtZSBSb290IENBMIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEAyicB\nQmnohxpFfMJW4pvWkd3IIbNJdX+UIsRMGKQdTKcWyPjwsJn62huNbCImO+J1VLpM\ng1Pk9EGyW+HihuLbH1xzIZQ36rWWf8ubKwbcV2mwnfGodMoyEQYjZ7n1Jy4mcSiX\nKFkC9/8SlHF5qXj5d+otHTndkhPXWK6JUeJYWQYhDqJu1e4Hmp9ykfzYDB16HWTt\nTNK4HZ2qUhYIzFMM+ax579WQM436HbtN0xSFz8HkBR0nRGylKs7dmXFEBm9fIEVb\nZaY4DsN6aHESHi/gHWirGyUfdNgRTXDf6A86stgEu+HMarbLmdiFRd0sEHaqiRK2\nteBsTXMyPEa2+A1epwIDAQABo1MwUTAdBgNVHQ4EFgQUW2Av9+n0y03L5tiEIpLH\ngAMcMecwHwYDVR0jBBgwFoAUW2Av9+n0y03L5tiEIpLHgAMcMecwDwYDVR0TAQH/\nBAUwAwEB/zANBgkqhkiG9w0BAQsFAAOCAQEABvNs0QvV0tW/8T42S5ZR9qCT5IRu\nFb9kuH1wIL4kckY6qWB2SS2Pw2DBS1+CXmfN6nYCfyQS40OrmxGIAa3xXfGgdZEn\nA+0bUk/JmnN62VDgNOIYJ5hhJ9Ku+EWwpMpiQuXNfa8xKG7NOI9T6ljdr2A0bFly\nsTnIrKzn4TJASeqhapTzScSD1W5N9g8OO4h96aHa1NfquNTZtPPLmDRAczesyBZc\nV4ff/J35zEwDgFwJfTMSBmkkGhENBSbE1iDPFAEm0ew+krP0DI/Jf9M6O+lh0B/+\nzKGcK7HPvuK4E1zSUapS3gosvYlEXl2Mj5mPKUCb3X3CJN5/XLs+lQCm5g==\n-----END CERTIFICATE-----\n"
  tlsClientCert:
    value: "-----BEGIN CERTIFICATE-----\nMIIDiTCCAnGgAwIBAgIUUy9I/W0Fp+ie3Wyx9IPTW/vXPLswDQYJKoZIhvcNAQEL\nBQAwUzELMAkGA1UEBhMCQ04xCzAJBgNVBAgMAkdEMQswCQYDVQQHDAJTWjETMBEG\nA1UECgwKQWNtZSwgSW5jLjEVMBMGA1UEAwwMQWNtZSBSb290IENBMB4XDTIzMDcx\nOTIyNTU1OFoXDTI0MDcxODIyNTU1OFowUDELMAkGA1UEBhMCQ04xCzAJBgNVBAgM\nAkdEMQswCQYDVQQHDAJTWjETMBEGA1UECgwKQWNtZSwgSW5jLjESMBAGA1UEAwwJ\nbG9jYWxob3N0MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEAiRUt3DDP\nuntymT64i4ZL81OkxNE1hgGc9PGfCOBjk5rP8LBgRa95maBTbK5GYNJDqPtD6AtV\npV3D2dtc4Q0CZ/8trFBn0cqYuNcho13f6y0Y8iH7M9RrAo9XZ1UAwhoST9GcB+w3\nZ4VRPsMNBblHJppa9BGgH7LqZEIZRPUCsn62ROFjxfx7pgvby276WjcP/eXLpnLV\nIpVTMStOGKci7Ul0oZkb141VHo/80uh6wzhW7tOZQDvhOgR/qe+O/p9PBFe+RTo/\n+YhWDKRdPtjbdEF7KiVuKHfQJR2grC9sH/K7m6S6828ipmw6kKcSqQfe+PUXhHyF\nQSspigkeycLLUwIDAQABo1gwVjAUBgNVHREEDTALgglsb2NhbGhvc3QwHQYDVR0O\nBBYEFJ0CtzmDzMw7CAbpubBTlzX52jePMB8GA1UdIwQYMBaAFFtgL/fp9MtNy+bY\nhCKSx4ADHDHnMA0GCSqGSIb3DQEBCwUAA4IBAQAOor9yXrGLItkUVX7PXUhnS7Cu\nA8W7tlAtLCk+MW3b3qGeoy19mr/q+olD8T1lfWG2K8DSHKwxfkGLGSxsz4/9iUhF\nujXDJrGA7znVWF3kElh/tlsAOiKZ/tE9XXiuK1hghcV/s0d2FgANZh5xmkudZa1m\nnqRJe36+bxWfL490u3L9lO3gQE7zlcvR5TNeRcWe7TyHGDkU3dYIzSmvX32jtKC3\nVUWU9Z8cmIy2mMOomdxlzMoSdOCw19hecgSidZArXTNo+uOgKFnYDp+RxC0DwK1f\n0tW1Zhudkuj3uz+5KdXzAQ8xGse5dNl/n2KMZPh/EqH/3EwGT2tmrkjB+ikx\n-----END CERTIFICATE-----\n"
  tlsClientKey:
    value: "-----BEGIN PRIVATE KEY-----\nMIIEvAIBADANBgkqhkiG9w0BAQEFAASCBKYwggSiAgEAAoIBAQCJFS3cMM+6e3KZ\nPriLhkvzU6TE0TWGAZz08Z8I4GOTms/wsGBFr3mZoFNsrkZg0kOo+0PoC1WlXcPZ\n21zhDQJn/y2sUGfRypi41yGjXd/rLRjyIfsz1GsCj1dnVQDCGhJP0ZwH7DdnhVE+\nww0FuUcmmlr0EaAfsupkQhlE9QKyfrZE4WPF/HumC9vLbvpaNw/95cumctUilVMx\nK04YpyLtSXShmRvXjVUej/zS6HrDOFbu05lAO+E6BH+p747+n08EV75FOj/5iFYM\npF0+2Nt0QXsqJW4od9AlHaCsL2wf8rubpLrzbyKmbDqQpxKpB9749ReEfIVBKymK\nCR7JwstTAgMBAAECggEACdHywUGH6AqG456AIZKjJzvDOSGUQtTOayenURSjXYQW\nHbPCOcu/xiHGSCoAkFgPu8UVkMPFxL0VANV7mlhQZ1B686CmR1D1pCwufzcENE3n\nPxVFxb6TqeZzMvZTwb6UuD4XqOOi9m9bqhzWoSXW3deUk25zI5eGvgbpgNdHya86\nRLZgEDGSnnAMZi7FANXR+gtvHQmXYP9CM/1rivYQI/MXd4jbZEVWeNZEc1u1YqVC\nM7ddN063KmE/LQewF2dHhw5dyhh1udgpoXuOFhmkbwBPLn6eVJ/GdKRG0+LDfOmW\nFx6QN7HV0gYISdVIUQBOsEPSh2LEurKDJyqzFLUoiQKBgQC+B08abD4D+3JQTmPV\nZuHusL3wizZxx4YQ1SNULMOv+i7oP22ERXsCCrnRGn+JPM/jc2HV0I3Z67BS9a2M\nUjRVZ54rN5DTWP+XAN1rCcDkxrp0CZ9pL4UZwcAZyanIIYCc0RcTHM//2WblLtHg\n60OJ5lMTlqc2LhQquwIM/5iAKwKBgQC4rFZYXoj6AYkdEyQSWcqOrtH72h5GZ9Rw\nmETRk8GeMI2gD6t92DWZmZcC+ycUGDxBX1A7fQ+imw31KUz0ciAX+aDWEgUBOgS5\n50KvRAo9JcHxyj5qXPlXpCHItaGjd2A1KexvKRxN4GngEouQcOTSF69pyP4P837v\nFfVclHgleQKBgHa8rmq+M8ndNcKEGNFhJc81VJmXAv/5QgYGh7qy9dthoimwaEf7\n+i5+cTj9K6+e9e8TS5CEbf46zMQpirVhMB4lTqmGYNIOjDXYboHRaFwK6LpUwOzC\nqpI7hEMYxSOt+2UBKT/iAv3E5AxUQwQdPOhPqJ1Wx0iNZkCE9XUVyD5dAoGADY6B\nLC2MaqXwNdUw4bP7vauxuIZTkKGQo89ZxfTN0toHm4dq9GsJzEPNJSfgv4Xj7nyb\nvDI7EpnFVYj5oDw8hOYLW4upGGT08dy7NXiOM9zwtto86Lv4hemDnWNQAVsDEgQI\n2kQvUrw1qYBTBIB1G8MnWmGp3OvoFo8LGwe/JikCgYA9RgIuXNkcYMPtRuOzX/ne\nCxP3enIwGgTfMZl7i4wprbv11FwoihhhkPjnY/tQikvmw6zTkqPrnALdDia15kmt\n0YpGrkkFOQ1RzPP4brOr17mcA2RQqfzWxq/trXiJVVIch7c4EBliUCH+qr7WMGUK\nb0K2SICFbq1Z4B4X9/MqGw==\n-----END PRIVATE KEY-----\n"
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
