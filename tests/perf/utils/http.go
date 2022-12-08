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
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

const (
	// DefaultProbeTimeout is the a timeout used in HTTPGetNTimes() and
	// HTTPGetRawNTimes() to avoid cases where early requests hang and
	// block all subsequent requests.
	DefaultProbeTimeout = 30 * time.Second
)

var httpClient *http.Client

func init() {
	httpClient = &http.Client{
		Transport: &http.Transport{
			Dial: (&net.Dialer{
				Timeout: DefaultProbeTimeout,
			}).Dial,
		},
	}
}

// HTTPGetNTimes calls the url n times and returns the first success or last error.
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

// HTTPGet is a helper to make GET request call to url
func HTTPGet(url string) ([]byte, error) {
	resp, err := httpClient.Get(sanitizeHTTPURL(url))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	return extractBody(resp.Body)
}

// HTTPPost is a helper to make POST request call to url
func HTTPPost(url string, data []byte) ([]byte, error) {
	resp, err := httpClient.Post(sanitizeHTTPURL(url), "application/json", bytes.NewBuffer(data))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	return extractBody(resp.Body)
}

// HTTPDelete calls a given URL with the HTTP DELETE method.
func HTTPDelete(url string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodDelete, sanitizeHTTPURL(url), nil)
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
	if !strings.Contains(url, "http") {
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
