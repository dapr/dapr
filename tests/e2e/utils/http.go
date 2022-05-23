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
	"fmt"
	"io"
	"net"
	"net/http"
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

func InitHTTPClient(enableHTTP2 bool) {
	if enableHTTP2 {
		httpClient = &http.Client{
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
		}
	} else {
		httpClient = &http.Client{
			Timeout: DefaultProbeTimeout,
			Transport: &http.Transport{
				MaxIdleConns:        2,
				MaxIdleConnsPerHost: 1,
			},
		}
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

	return extractBody(resp.Body)
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
	return httpClient.Get(sanitizeHTTPURL(url))
}

// HTTPPost is a helper to make POST request call to url.
func HTTPPost(url string, data []byte) ([]byte, error) {
	resp, err := httpClient.Post(sanitizeHTTPURL(url), "application/json", bytes.NewBuffer(data))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	return extractBody(resp.Body)
}

// HTTPPatch is a helper to make PATCH request call to url.
func HTTPPatch(url string, data []byte) ([]byte, error) {
	req, err := http.NewRequest("PATCH", sanitizeHTTPURL(url), bytes.NewBuffer(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	return extractBody(resp.Body)
}

// HTTPPostWithStatus is a helper to make POST request call to url.
func HTTPPostWithStatus(url string, data []byte) ([]byte, int, error) {
	resp, err := httpClient.Post(sanitizeHTTPURL(url), "application/json", bytes.NewBuffer(data))
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

	body, err := extractBody(resp.Body)

	return body, resp.StatusCode, err
}

// HTTPDelete calls a given URL with the HTTP DELETE method.
func HTTPDelete(url string) ([]byte, error) {
	req, err := http.NewRequest("DELETE", sanitizeHTTPURL(url), nil)
	if err != nil {
		return nil, err
	}

	res, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	body, err := extractBody(res.Body)
	if err != nil {
		return nil, err
	}

	return body, nil
}

func sanitizeHTTPURL(url string) string {
	if !strings.HasPrefix(url, "http") {
		url = fmt.Sprintf("http://%s", url)
	}

	return url
}

func extractBody(r io.ReadCloser) ([]byte, error) {
	body, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}

	return body, nil
}
