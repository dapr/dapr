// +build e2e

// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package e2e

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"
)

func httpGet(url string) ([]byte, error) {
	if !strings.Contains(url, "http") {
		url = fmt.Sprintf("http://%s", url)
	}

	resp, err := http.Get(url)
	defer resp.Body.Close()
	if err != nil {
		return nil, err
	}
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return body, nil
}
