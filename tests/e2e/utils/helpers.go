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
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	guuid "github.com/google/uuid"
)

const (
	// Environment variable for setting the target OS where tests are running on.
	TargetOsEnvVar = "TARGET_OS"

	// Max number of healthcheck calls before starting tests.
	numHealthChecks = 60
)

// SimpleKeyValue can be used to simplify code, providing simple key-value pairs.
type SimpleKeyValue struct {
	Key   any
	Value any
}

// StateTransactionKeyValue is a key-value pair with an operation type.
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

// TestTargetOS returns the name of the OS that the tests are targeting (which could be different from the local OS).
func TestTargetOS() string {
	// Check if we have an env var first
	if v, ok := os.LookupEnv(TargetOsEnvVar); ok {
		return v
	}
	// Fallback to the runtime
	return runtime.GOOS
}

// FormatDuration formats the duration in ms
func FormatDuration(d time.Duration) string {
	return fmt.Sprintf("%dms", d.Truncate(100*time.Microsecond).Milliseconds())
}

// IsTruthy returns true if a string is a truthy value.
// Truthy values are "y", "yes", "true", "t", "on", "1" (case-insensitive); everything else is false.
func IsTruthy(val string) bool {
	switch strings.ToLower(strings.TrimSpace(val)) {
	case "y", "yes", "true", "t", "on", "1":
		return true
	default:
		return false
	}
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
