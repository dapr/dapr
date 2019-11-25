// +build e2e

// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package e2e

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

func newHttpClient() http.Client {
	return http.Client{
		Transport: &http.Transport{
			// Sometimes, the first connection to ingress endpoint takes longer than 1 minute (e.g. AKS)
			Dial: (&net.Dialer{
				Timeout: 5 * time.Minute,
			}).Dial,
		},
	}
}

func httpGet(url string) ([]byte, error) {
	client := newHttpClient()
	resp, err := client.Get(validUrl(url))
	if err != nil {
		return nil, err
	}

	return extractBody(resp.Body)
}

func httpPost(url string, data []byte) ([]byte, error) {
	client := newHttpClient()
	resp, err := client.Post(validUrl(url), "application/json", bytes.NewBuffer(data))
	if err != nil {
		return nil, err
	}

	return extractBody(resp.Body)
}

func validUrl(url string) string {
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
