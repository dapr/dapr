/*
Copyright 2024 The Dapr Authors
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

package errors

import (
	"fmt"
	"net/http"

	"google.golang.org/grpc/codes"

	"github.com/dapr/dapr/pkg/messages/errorcodes"
	"github.com/dapr/kit/errors"
)

// ConfigurationError builds rich errors for the Configuration API following the
// gRPC richer error model.
type ConfigurationError struct {
	name             string
	skipResourceInfo bool
}

// Configuration returns a builder for Configuration API errors for the given store name.
func Configuration(name string) *ConfigurationError {
	return &ConfigurationError{
		name: name,
	}
}

// NotConfigured is returned when no configuration stores are configured.
func (c *ConfigurationError) NotConfigured() error {
	c.skipResourceInfo = true
	return c.build(
		errors.NewBuilder(
			codes.FailedPrecondition,
			http.StatusInternalServerError,
			"configuration stores not configured",
			errorcodes.ConfigurationStoreNotConfigured.Code,
			string(errorcodes.ConfigurationStoreNotConfigured.Category),
		),
		errorcodes.ConfigurationStoreNotConfigured.GrpcCode,
		nil,
	)
}

// NotFound is returned when the requested configuration store does not exist.
func (c *ConfigurationError) NotFound() error {
	c.skipResourceInfo = true
	return c.build(
		errors.NewBuilder(
			codes.InvalidArgument,
			http.StatusBadRequest,
			fmt.Sprintf("configuration store %s not found", c.name),
			errorcodes.ConfigurationStoreNotFound.Code,
			string(errorcodes.ConfigurationStoreNotFound.Category),
		),
		errorcodes.ConfigurationStoreNotFound.GrpcCode,
		nil,
	)
}

// GetFailed is returned when reading configuration items from the store fails.
func (c *ConfigurationError) GetFailed(keys []string, reason string) error {
	return c.build(
		errors.NewBuilder(
			codes.Internal,
			http.StatusInternalServerError,
			fmt.Sprintf("failed to get %v from Configuration store %s: %s", keys, c.name, reason),
			errorcodes.ConfigurationGet.Code,
			string(errorcodes.ConfigurationGet.Category),
		),
		errorcodes.ConfigurationGet.GrpcCode,
		nil,
	)
}

// SubscribeFailed is returned when subscribing to configuration updates fails.
func (c *ConfigurationError) SubscribeFailed(keys []string, reason string) error {
	return c.build(
		errors.NewBuilder(
			codes.InvalidArgument,
			http.StatusInternalServerError,
			fmt.Sprintf("failed to subscribe %v from Configuration store %s: %s", keys, c.name, reason),
			errorcodes.ConfigurationSubscribe.Code,
			string(errorcodes.ConfigurationSubscribe.Category),
		),
		errorcodes.ConfigurationSubscribe.GrpcCode,
		nil,
	)
}

// UnsubscribeFailed is returned when cancelling a configuration subscription fails.
func (c *ConfigurationError) UnsubscribeFailed(subscribeID string, reason string) error {
	return c.build(
		errors.NewBuilder(
			codes.InvalidArgument,
			http.StatusInternalServerError,
			fmt.Sprintf("failed to unsubscribe to configuration request %s: %s", subscribeID, reason),
			errorcodes.ConfigurationUnsubscribe.Code,
			string(errorcodes.ConfigurationUnsubscribe.Category),
		),
		errorcodes.ConfigurationUnsubscribe.GrpcCode,
		nil,
	)
}

func (c *ConfigurationError) build(err *errors.ErrorBuilder, errCode string, metadata map[string]string) error {
	if !c.skipResourceInfo {
		err = err.WithResourceInfo("configuration", c.name, "", "")
	}
	return err.
		WithErrorInfo(errCode, metadata).
		Build()
}
