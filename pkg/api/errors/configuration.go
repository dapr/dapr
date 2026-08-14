/*
Copyright 2026 The Dapr Authors
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
	kiterrors "github.com/dapr/kit/errors"
)

const configurationStoreComponentType = "configuration"

// ConfigurationError builds standardized errors for the Configuration API.
type ConfigurationError struct {
	storeName        string
	skipResourceInfo bool
}

// Configuration returns a ConfigurationError scoped to the given store name.
func Configuration(storeName string) *ConfigurationError {
	return &ConfigurationError{storeName: storeName}
}

// StoreNotConfigured returns a standardized error when no configuration stores are configured.
func (c *ConfigurationError) StoreNotConfigured() error {
	msg := "configuration stores not configured"
	return c.build(
		kiterrors.NewBuilder(
			codes.FailedPrecondition,
			http.StatusInternalServerError,
			msg,
			errorcodes.ConfigurationStoreNotConfigured.Code,
			string(errorcodes.ConfigurationStoreNotConfigured.Category),
		),
		errorcodes.ConfigurationStoreNotConfigured.GrpcCode,
		nil,
	)
}

// StoreNotFound returns a standardized error when the named configuration store is not found.
func (c *ConfigurationError) StoreNotFound() error {
	msg := fmt.Sprintf("configuration store %s not found", c.storeName)
	return c.build(
		kiterrors.NewBuilder(
			codes.InvalidArgument,
			http.StatusInternalServerError,
			msg,
			errorcodes.ConfigurationStoreNotFound.Code,
			string(errorcodes.ConfigurationStoreNotFound.Category),
		),
		errorcodes.ConfigurationStoreNotFound.GrpcCode,
		nil,
	)
}

// GetFailed returns a standardized error for a failed configuration get operation.
// The gRPC status message is kept identical to the legacy messages.ErrConfigurationGet
// value for backwards compatibility.
func (c *ConfigurationError) GetFailed(keys []string, err error) error {
	msg := fmt.Sprintf("failed to get %s from Configuration store %s: %v", keys, c.storeName, err)
	return c.build(
		kiterrors.NewBuilder(
			codes.Internal,
			http.StatusInternalServerError,
			msg,
			errorcodes.ConfigurationGet.Code,
			string(errorcodes.ConfigurationGet.Category),
		),
		errorcodes.ConfigurationGet.GrpcCode,
		map[string]string{"error": err.Error()},
	)
}

// SubscribeFailed returns a standardized error for a failed configuration subscribe operation.
// The gRPC status message is kept identical to the legacy messages.ErrConfigurationSubscribe
// value for backwards compatibility.
func (c *ConfigurationError) SubscribeFailed(keys []string, err error) error {
	msg := fmt.Sprintf("failed to subscribe %s from Configuration store %s: %v", keys, c.storeName, err)
	return c.build(
		kiterrors.NewBuilder(
			codes.InvalidArgument,
			http.StatusInternalServerError,
			msg,
			errorcodes.ConfigurationSubscribe.Code,
			string(errorcodes.ConfigurationSubscribe.Category),
		),
		errorcodes.ConfigurationSubscribe.GrpcCode,
		map[string]string{"error": err.Error()},
	)
}

// build attaches ResourceInfo (unless skipped) and ErrorInfo to the error so the
// per-method constructors don't repeat the boilerplate (mirrors pkg/api/errors/state.go).
func (c *ConfigurationError) build(b *kiterrors.ErrorBuilder, errCode string, metadata map[string]string) error {
	if !c.skipResourceInfo {
		b = b.WithResourceInfo(configurationStoreComponentType, c.storeName, "", "")
	}
	return b.WithErrorInfo(errCode, metadata).Build()
}
