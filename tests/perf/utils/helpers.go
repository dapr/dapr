// +build perf

// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package utils

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	guuid "github.com/google/uuid"

	"github.com/dapr/components-contrib/bindings"
	"github.com/dapr/components-contrib/bindings/azure/blobstorage"
	"github.com/dapr/dapr/tests/perf"
	"github.com/dapr/kit/logger"
)

// SimpleKeyValue can be used to simplify code, providing simple key-value pairs.
type SimpleKeyValue struct {
	Key   interface{}
	Value interface{}
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

func newHTTPClient() http.Client {
	return http.Client{
		Transport: &http.Transport{
			// Sometimes, the first connection to ingress endpoint takes longer than 1 minute (e.g. AKS)
			Dial: (&net.Dialer{
				// This number cannot be large. Callers should retry failed calls (see HTTPGetNTimes())
				Timeout: 3 * time.Minute,
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

// HTTPGet is a helper to make GET request call to url
func HTTPGet(url string) ([]byte, error) {
	client := newHTTPClient()
	resp, err := client.Get(sanitizeHTTPURL(url)) //nolint
	if err != nil {
		return nil, err
	}

	return extractBody(resp.Body)
}

// HTTPGetRawNTimes calls the url n times and returns the first success or last error.
func HTTPGetRawNTimes(url string, n int) (*http.Response, error) {
	var res *http.Response
	var err error
	for i := n - 1; i >= 0; i-- {
		res, err = HTTPGetRaw(url)
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

// HTTPGetRaw is a helper to make GET request call to url
func HTTPGetRaw(url string) (*http.Response, error) {
	client := newHTTPClient()
	resp, err := client.Get(sanitizeHTTPURL(url)) //nolint
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
	if err != nil {
		return nil, err
	}

	return body, nil
}

// UploadAzureBlob takes test output data and saves it to an Azure Blob Storage container
func UploadAzureBlob(report *perf.TestReport) error {
	accountName, accountKey := os.Getenv("AZURE_STORAGE_ACCOUNT"), os.Getenv("AZURE_STORAGE_ACCESS_KEY")
	if len(accountName) == 0 || len(accountKey) == 0 {
		return nil
	}

	now := time.Now().UTC()
	y := now.Year()
	m := now.Month()
	d := now.Day()

	container := fmt.Sprintf("%v-%v-%v", int(m), d, y)

	b, err := json.Marshal(report)
	if err != nil {
		return err
	}

	l := logger.NewLogger("dapr-perf-test")
	azblob := blobstorage.NewAzureBlobStorage(l)

	err = azblob.Init(bindings.Metadata{
		Properties: map[string]string{
			"storageAccount":    accountName,
			"storageAccessKey":  accountKey,
			"container":         container,
			"publicAccessLevel": "container",
		},
	})
	if err != nil {
		return err
	}

	filename := fmt.Sprintf("%s-%v-%v-%v", report.TestName, now.Hour(), time.Hour.Minutes(), time.Hour.Seconds())
	_, err = azblob.Invoke(&bindings.InvokeRequest{
		Operation: bindings.CreateOperation,
		Data:      b,
		Metadata: map[string]string{
			"blobName":    filename,
			"ContentType": "application/json",
		},
	})
	return err
}

func sanitizeHTTPURL(url string) string {
	if !strings.Contains(url, "http") {
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
