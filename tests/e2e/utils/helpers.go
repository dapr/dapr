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
	"time"
)

func newHTTPClient() http.Client {
	return http.Client{
		Transport: &http.Transport{
			// Sometimes, the first connection to ingress endpoint takes longer than 1 minute (e.g. AKS)
			Dial: (&net.Dialer{
				Timeout: 5 * time.Minute,
			}).Dial,
		},
	}
}

// HTTPGet is a helper to make GET request call to url
func HTTPGet(url string) ([]byte, error) {
	client := newHTTPClient()
	resp, err := client.Get(sanitizeHTTPURL(url))
	if err != nil {
		return nil, err
	}

	return extractBody(resp.Body)
}

// HTTPPost is a helper to make POST request call to url
func HTTPPost(url string, data []byte) ([]byte, error) {
	client := newHTTPClient()
	resp, err := client.Post(sanitizeHTTPURL(url), "application/json", bytes.NewBuffer(data))
	if err != nil {
		return nil, err
	}

	return extractBody(resp.Body)
}

func sanitizeHTTPURL(url string) string {
	if !strings.Contains(url, "http") {
		url = fmt.Sprintf("http://%s", url)
	}

	return url
}

func extractBody(r io.ReadCloser) ([]byte, error) {
	body, err := ioutil.ReadAll(r)
	if err != nil {
		return nil, err
	}

	r.Close()

	return body, nil
}
