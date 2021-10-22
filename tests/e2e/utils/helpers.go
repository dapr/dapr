// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package utils

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	guuid "github.com/google/uuid"
)

var (
	doOnce        sync.Once
	defaultClient http.Client
)

// DefaultProbeTimeout is the a timeout used in HTTPGetNTimes() and
// HTTPGetRawNTimes() to avoid cases where early requests hang and
// block all subsequent requests.
const DefaultProbeTimeout = 30 * time.Second

// SimpleKeyValue can be used to simplify code, providing simple key-value pairs.
type SimpleKeyValue struct {
	Key   interface{}
	Value interface{}
}

// StateTransactionKeyValue is a key-value pair with an operation type.
type StateTransactionKeyValue struct {
	Key           string
	Value         string
	OperationType string
}

var httpClient = newHTTPClient()

// GenerateRandomStringKeys generates random string keys (values are nil).
func GenerateRandomStringKeys(num int) []SimpleKeyValue {
	if num < 0 {
		return make([]SimpleKeyValue, 0)
	}

	output := make([]SimpleKeyValue, 0, num)
	for i := 1; i <= num; i++ {
		key := guuid.New().String()
		output = append(output, SimpleKeyValue{key, nil})
	}

	return output
}

// GenerateRandomStringValues sets random string values for the keys passed in.
func GenerateRandomStringValues(keyValues []SimpleKeyValue) []SimpleKeyValue {
	output := make([]SimpleKeyValue, 0, len(keyValues))
	for i, keyValue := range keyValues {
		key := keyValue.Key
		value := fmt.Sprintf("Value for entry #%d with key %v.", i+1, key)
		output = append(output, SimpleKeyValue{key, value})
	}

	return output
}

// GenerateRandomStringKeyValues generates random string key-values pairs.
func GenerateRandomStringKeyValues(num int) []SimpleKeyValue {
	keys := GenerateRandomStringKeys(num)
	return GenerateRandomStringValues(keys)
}

func newHTTPClient() http.Client {
	doOnce.Do(func() {
		defaultClient = http.Client{
			Timeout: time.Second * 15,
			Transport: &http.Transport{
				// Sometimes, the first connection to ingress endpoint takes longer than 1 minute (e.g. AKS)
				Dial: (&net.Dialer{
					Timeout:   5 * time.Minute,
					KeepAlive: 6 * time.Minute,
				}).Dial,
			},
		}
	})

	return defaultClient
}

// HTTPGetNTimes calls the url n times and returns the first success
// or last error.
//
// Since this is used to probe when servers are starting up, we want
// to use a smaller timeout value here to avoid early requests, if
// hanging, from blocking all subsequent ones.
func HTTPGetNTimes(url string, n int) ([]byte, error) {
	var res []byte
	var err error
	for i := n - 1; i >= 0; i-- {
		res, err = httpGet(url, DefaultProbeTimeout)
		if i == 0 {
			break
		}

		if err != nil {
			time.Sleep(time.Second)
		} else {
			return res, nil
		}
	}

	return res, err
}

// httpGet is a helper to make GET request call to url.
func httpGet(url string, timeout time.Duration) ([]byte, error) {
	resp, err := httpGetRaw(url, timeout) //nolint
	if err != nil {
		return nil, err
	}

	return extractBody(resp.Body)
}

// HTTPGet is a helper to make GET request call to url.
func HTTPGet(url string) ([]byte, error) {
	return httpGet(url, 0 /* no timeout */)
}

// HTTPGetRawNTimes calls the url n times and returns the first
// success or last error.
//
// Since this is used to probe when servers are starting up, we want
// to use a smaller timeout value here to avoid early requests, if
// hanging, from blocking all subsequent ones.
func HTTPGetRawNTimes(url string, n int) (*http.Response, error) {
	var res *http.Response
	var err error
	for i := n - 1; i >= 0; i-- {
		res, err = httpGetRaw(url, DefaultProbeTimeout)
		if i == 0 {
			break
		}

		if err != nil {
			time.Sleep(time.Second)
		} else {
			return res, nil
		}
	}

	return res, err
}

// HTTPGetRaw is a helper to make GET request call to url.
func httpGetRaw(url string, t time.Duration) (*http.Response, error) {
	if t != 0 {
		httpClient.Timeout = t
	}
	resp, err := httpClient.Get(sanitizeHTTPURL(url))
	if err != nil {
		return nil, err
	}
	return resp, nil
}

// HTTPGetRaw is a helper to make GET request call to url.
func HTTPGetRaw(url string) (*http.Response, error) {
	return httpGetRaw(url, 0)
}

// HTTPPost is a helper to make POST request call to url.
func HTTPPost(url string, data []byte) ([]byte, error) {
	resp, err := httpClient.Post(sanitizeHTTPURL(url), "application/json", bytes.NewBuffer(data)) //nolint
	if err != nil {
		return nil, err
	}

	return extractBody(resp.Body)
}

// HTTPPostWithStatus is a helper to make POST request call to url.
func HTTPPostWithStatus(url string, data []byte) ([]byte, int, error) {
	resp, err := httpClient.Post(sanitizeHTTPURL(url), "application/json", bytes.NewBuffer(data)) //nolint
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

	body, err := extractBody(res.Body)
	defer res.Body.Close()
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
	if r != nil {
		defer r.Close()
	}

	body, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}

	return body, nil
}
