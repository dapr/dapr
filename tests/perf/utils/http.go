package utils

import (
	"bytes"
	"fmt"
	"io"
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
		Timeout: DefaultProbeTimeout,
	}
}

// HTTPGetNTimes calls the url n times and returns the first success or last error.
func HTTPGetNTimes(url string, n int) ([]byte, error) {
	var res []byte
	var err error
	for i := n - 1; i >= 0; i-- {
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
	resp, err := httpClient.Get(sanitizeHTTPURL(url)) //nolint
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	return extractBody(resp.Body)
}

// HTTPPost is a helper to make POST request call to url
func HTTPPost(url string, data []byte) ([]byte, error) {
	resp, err := httpClient.Post(sanitizeHTTPURL(url), "application/json", bytes.NewBuffer(data)) //nolint
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	return extractBody(resp.Body)
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
