/*
Copyright 2021 The Dapr Authors
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

package utils

import (
	"bytes"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"golang.org/x/net/http2"
)

const (
	// DefaultProbeTimeout is the a timeout used in HTTPGetNTimes() and
	// HTTPGetRawNTimes() to avoid cases where early requests hang and
	// block all subsequent requests.
	DefaultProbeTimeout = 30 * time.Second
)

var httpClient *http.Client

// InitHTTPClient inits the shared httpClient object.
func InitHTTPClient(allowHTTP2 bool) {
	httpClient = NewHTTPClient(allowHTTP2)
}

// NewHTTPClient initializes a new *http.Client.
// This should not be used except in rare circumstances. Developers should use the shared httpClient instead to re-use sockets as much as possible.
func NewHTTPClient(allowHTTP2 bool) *http.Client {
	// HTTP/2 is allowed only if the DAPR_TESTS_HTTP2 env var is set
	allowHTTP2 = allowHTTP2 && IsTruthy(os.Getenv("DAPR_TESTS_HTTP2"))

	if allowHTTP2 {
		return &http.Client{
			Timeout: DefaultProbeTimeout,
			// Configure for HTT/2 Cleartext (without TLS) and with prior knowledge
			// (RFC7540 Section 3.2)
			Transport: &http2.Transport{
				// Make the transport accept scheme "http:"
				AllowHTTP: true,
				// Pretend we are dialing a TLS endpoint
				DialTLS: func(network, addr string, cfg *tls.Config) (net.Conn, error) {
					return net.Dial(network, addr)
				},
			},
			// disable test app client auto redirect handle
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
	}
	return &http.Client{
		Timeout: DefaultProbeTimeout,
		Transport: &http.Transport{
			MaxIdleConns:        2,
			MaxIdleConnsPerHost: 1,
		},
		// disable test app client auto redirect handle
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// HTTPGetNTimes calls the url n times and returns the first success
// or last error.
func HTTPGetNTimes(url string, n int) ([]byte, error) {
	var res []byte
	var err error
	for i := n - 1; i >= 0; i-- {
		res, err = HTTPGet(url)
		if err == nil {
			return res, nil
		}

		if i == 0 {
			break
		}

		time.Sleep(time.Second)
	}

	return res, err
}

// HTTPGet is a helper to make GET request call to url.
func HTTPGet(url string) ([]byte, error) {
	resp, err := HTTPGetRaw(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	return io.ReadAll(resp.Body)
}

// HTTPGetWithStatus is a helper to make GET request call to url.
func HTTPGetWithStatus(url string) ([]byte, int, error) {
	resp, err := HTTPGetRaw(url)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, 0, err
	}

	return body, resp.StatusCode, nil
}

// HTTPGetWithStatusWithData is a helper to make GET request call to url.
func HTTPGetWithStatusWithData(surl string, data []byte) ([]byte, int, error) {
	url, err := url.Parse(SanitizeHTTPURL(surl))
	if err != nil {
		return nil, 0, err
	}
	resp, err := httpClient.Do(&http.Request{
		Method: http.MethodGet,
		URL:    url,
		Body:   io.NopCloser(bytes.NewReader(data)),
	})
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, 0, err
	}

	return body, resp.StatusCode, nil
}

// HTTPGetRawNTimes calls the url n times and returns the first
// success or last error.
func HTTPGetRawNTimes(url string, n int) (*http.Response, error) {
	var res *http.Response
	var err error
	for i := n - 1; i >= 0; i-- {
		res, err = HTTPGetRaw(url)
		if err == nil {
			return res, nil
		}

		if res != nil && res.Body != nil {
			// Drain before closing
			_, _ = io.Copy(io.Discard, res.Body)
			_ = res.Body.Close()
		}

		if i == 0 {
			break
		}
		time.Sleep(time.Second)
	}

	return res, err
}

// HTTPGetRaw is a helper to make GET request call to url.
func HTTPGetRaw(url string) (*http.Response, error) {
	return httpClient.Get(SanitizeHTTPURL(url))
}

// HTTPPost is a helper to make POST request call to url.
func HTTPPost(url string, data []byte) ([]byte, error) {
	resp, err := httpClient.Post(SanitizeHTTPURL(url), "application/json", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	return io.ReadAll(resp.Body)
}

// HTTPPatch is a helper to make PATCH request call to url.
func HTTPPatch(url string, data []byte) ([]byte, error) {
	req, err := http.NewRequest(http.MethodPatch, SanitizeHTTPURL(url), bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	return io.ReadAll(resp.Body)
}

// HTTPPostWithStatus is a helper to make POST request call to url.
func HTTPPostWithStatus(url string, data []byte) ([]byte, int, error) {
	resp, err := httpClient.Post(SanitizeHTTPURL(url), "application/json", bytes.NewReader(data))
	if err != nil {
		// From the Do method for the client.Post
		// An error is returned if caused by client policy (such as
		// CheckRedirect), or failure to speak HTTP (such as a network
		// connectivity problem). A non-2xx status code doesn't cause an
		// error.
		if resp != nil {
			return nil, resp.StatusCode, err
		}
		return nil, http.StatusInternalServerError, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)

	return body, resp.StatusCode, err
}

// HTTPDelete calls a given URL with the HTTP DELETE method.
func HTTPDelete(url string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodDelete, SanitizeHTTPURL(url), nil)
	if err != nil {
		return nil, err
	}

	res, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	return io.ReadAll(res.Body)
}

// HTTPDeleteWithStatus calls a given URL with the HTTP DELETE method.
func HTTPDeleteWithStatus(url string) ([]byte, int, error) {
	req, err := http.NewRequest(http.MethodDelete, SanitizeHTTPURL(url), nil)
	if err != nil {
		return nil, 0, err
	}

	res, err := httpClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, 0, err
	}

	return body, res.StatusCode, nil
}

// SanitizeHTTPURL prepends the prefix "http://" to a URL if not present
func SanitizeHTTPURL(url string) string {
	if !strings.HasPrefix(url, "http") {
		url = "http://" + url
	}

	return url
}
