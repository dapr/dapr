//go:build perf
// +build perf

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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	guuid "github.com/google/uuid"

	"github.com/dapr/components-contrib/bindings"
	"github.com/dapr/components-contrib/bindings/azure/blobstorage"
	"github.com/dapr/components-contrib/metadata"
	"github.com/dapr/dapr/tests/perf"
	"github.com/dapr/kit/logger"
)

// Max number of healthcheck calls before starting tests.
const numHealthChecks = 60

// SimpleKeyValue can be used to simplify code, providing simple key-value pairs.
type SimpleKeyValue struct {
	Key   any
	Value any
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

	err = azblob.Init(context.Background(), bindings.Metadata{
		Base: metadata.Base{
			Properties: map[string]string{
				"storageAccount":    accountName,
				"storageAccessKey":  accountKey,
				"container":         container,
				"publicAccessLevel": "container",
			},
		},
	})
	if err != nil {
		return err
	}

	filename := fmt.Sprintf("%s-%v-%v-%v", report.TestName, now.Hour(), time.Hour.Minutes(), time.Hour.Seconds())
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	_, err = azblob.Invoke(ctx, &bindings.InvokeRequest{
		Operation: bindings.CreateOperation,
		Data:      b,
		Metadata: map[string]string{
			"blobName":    filename,
			"ContentType": "application/json",
		},
	})
	return err
}

// HealthCheckApps performs healthchecks for multiple apps, waiting for them to be ready.
func HealthCheckApps(urls ...string) error {
	count := len(urls)
	if count == 0 {
		return nil
	}

	// Run the checks in parallel
	errCh := make(chan error, count)
	for _, u := range urls {
		go func(u string) {
			_, err := HTTPGetNTimes(u, numHealthChecks)
			errCh <- err
		}(u)
	}

	// Collect all errors
	errs := make([]error, count)
	for i := 0; i < count; i++ {
		errs[i] = <-errCh
	}

	// Will be nil if no error
	return errors.Join(errs...)
}
