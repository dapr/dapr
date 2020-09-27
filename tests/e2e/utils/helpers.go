// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package utils

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	guuid "github.com/google/uuid"
)

var (
	doOnce             sync.Once
	defaultClient      http.Client
	defaultProbeClient http.Client
)

// SimpleKeyValue can be used to simplify code, providing simple key-value pairs.
type SimpleKeyValue struct {
	Key   interface{}
	Value interface{}
}

// StateTransactionKeyValue is a key-value pair with an operation type
type StateTransactionKeyValue struct {
	Key           string
	Value         string
	OperationType string
}

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

// We use this http.Client for the single-shot HTTPxxx()
// calls. Currently it specifies no timeout since some tests have
// really long running requests.
func newHTTPClient() http.Client {
	doOnce.Do(func() {
		defaultClient = http.Client{
			Transport: &http.Transport{
				// Sometimes, the first connection to ingress endpoint takes longer than 1 minute (e.g. AKS)
				Dial: (&net.Dialer{
					Timeout: 5 * time.Minute,
				}).Dial,
			},
		}
	})

	return defaultClient
}

// We use this http.Client for the HTTPxxxNTimes() calls. It specifies
// a shorter timeout, to avoid having early requests blocking
// subsequent ones if they hang.
func newHTTPProbeClient() http.Client {
	doOnce.Do(func() {
		defaultProbeClient = http.Client{
			Transport: &http.Transport{
				// Sometimes, the first connection to ingress endpoint takes longer than 1 minute (e.g. AKS)
				Dial: (&net.Dialer{
					Timeout: 5 * time.Minute,
				}).Dial,
			},
			Timeout: 10 * time.Second,
		}
	})

	return defaultProbeClient
}

// HTTPGetNTimes calls the url n times and returns the first success
// or last error.
//
// It is intended to use for probing until a server is up and
// receiving traffic, hence setting a shorter timeout to avoid early
// requests from hanging and block the rest of them.
func HTTPGetNTimes(url string, n int) ([]byte, error) {
	var res []byte
	var err error
	for i := n - 1; i >= 0; i-- {
		res, err = httpGet(newHTTPProbeClient(), url)
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

// httpGet is a helper to make GET request call to url
func httpGet(client http.Client, url string) ([]byte, error) {
	resp, err := httpGetRaw(client, url)
	if err != nil {
		return nil, err
	}
	return extractBody(resp.Body)
}

// HTTPGet is a helper to make GET request call to url
func HTTPGet(url string) ([]byte, error) {
	return httpGet(newHTTPClient(), url)
}

// HTTPGetRawNTimes calls the url n times and returns the first
// success or last error.
//
// It is intended to use for probing until a server is up and
// receiving traffic, hence setting a shorter timeout to avoid early
// requests from hanging and block the rest of them.
func HTTPGetRawNTimes(url string, n int) (*http.Response, error) {
	var res *http.Response
	var err error
	for i := n - 1; i >= 0; i-- {
		res, err = httpGetRaw(newHTTPProbeClient(), url)
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

// httpGetRaw is a helper to make GET request call to url
func httpGetRaw(client http.Client, url string) (*http.Response, error) {
	resp, err := client.Get(sanitizeHTTPURL(url))
	if err != nil {
		return nil, err
	}

	return resp, nil
}

// HTTPGetRaw is a helper to make GET request call to url
func HTTPGetRaw(url string) (*http.Response, error) {
	client := newHTTPClient()
	resp, err := client.Get(sanitizeHTTPURL(url))
	if err != nil {
		return nil, err
	}

	return resp, nil
}

// HTTPPost is a helper to make POST request call to url
func HTTPPost(url string, data []byte) ([]byte, error) {
	client := newHTTPClient()
	resp, err := client.Post(sanitizeHTTPURL(url), "application/json", bytes.NewBuffer(data)) //nolint
	if err != nil {
		return nil, err
	}

	return extractBody(resp.Body)
}

// HTTPPostWithStatus is a helper to make POST request call to url
func HTTPPostWithStatus(url string, data []byte) ([]byte, int, error) {
	client := newHTTPClient()
	resp, err := client.Post(sanitizeHTTPURL(url), "application/json", bytes.NewBuffer(data)) //nolint
	if err != nil {
		// From the Do method for the client.Post
		// An error is returned if caused by client policy (such as
		// CheckRedirect), or failure to speak HTTP (such as a network
		// connectivity problem). A non-2xx status code doesn't cause an
		// error.
		return nil, http.StatusInternalServerError, err
	}

	body, err := extractBody(resp.Body)

	return body, resp.StatusCode, err
}

// HTTPDelete calls a given URL with the HTTP DELETE method.
func HTTPDelete(url string) ([]byte, error) {
	client := newHTTPClient()

	req, err := http.NewRequest("DELETE", sanitizeHTTPURL(url), nil)
	if err != nil {
		return nil, err
	}

	res, err := client.Do(req)
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

	body, err := ioutil.ReadAll(r)
	if err != nil {
		return nil, err
	}

	return body, nil
}
