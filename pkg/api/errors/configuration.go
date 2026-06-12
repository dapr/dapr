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
	kiterrors "github.com/dapr/kit/errors"
)

const configurationStoreComponentType = "configuration"

// ConfigurationError builds standardized errors for the Configuration API.
type ConfigurationError struct {
	storeName string
}

// Configuration returns a ConfigurationError scoped to the given store name.
func Configuration(storeName string) *ConfigurationError {
	return &ConfigurationError{storeName: storeName}
}

// StoreNotConfigured returns a standardized error when no configuration stores are configured.
func (c *ConfigurationError) StoreNotConfigured() error {
	msg := "configuration stores not configured"
	return kiterrors.NewBuilder(
		codes.FailedPrecondition,
		http.StatusInternalServerError,
		msg,
		errorcodes.ConfigurationStoreNotConfigured.Code,
		string(errorcodes.ConfigurationStoreNotConfigured.Category),
	).
		WithErrorInfo(errorcodes.ConfigurationStoreNotConfigured.GrpcCode, nil).
		Build()
}

// StoreNotFound returns a standardized error when the named configuration store is not found.
func (c *ConfigurationError) StoreNotFound() error {
	msg := fmt.Sprintf("configuration store %s not found", c.storeName)
	return kiterrors.NewBuilder(
		codes.InvalidArgument,
		http.StatusBadRequest,
		msg,
		errorcodes.ConfigurationStoreNotFound.Code,
		string(errorcodes.ConfigurationStoreNotFound.Category),
	).
		WithResourceInfo(configurationStoreComponentType, c.storeName, "", msg).
		WithErrorInfo(errorcodes.ConfigurationStoreNotFound.GrpcCode, map[string]string{
			"store": c.storeName,
		}).
		Build()
}

// GetFailed returns a standardized error for a failed configuration get operation.
func (c *ConfigurationError) GetFailed(keys []string, err error) error {
	msg := fmt.Sprintf("error getting configuration with keys=%v from store %s: %s", keys, c.storeName, err)
	return kiterrors.NewBuilder(
		codes.Internal,
		http.StatusInternalServerError,
		msg,
		errorcodes.ConfigurationGet.Code,
		string(errorcodes.ConfigurationGet.Category),
	).
		WithResourceInfo(configurationStoreComponentType, c.storeName, "", msg).
		WithErrorInfo(errorcodes.ConfigurationGet.GrpcCode, map[string]string{
			"store": c.storeName,
			"error": err.Error(),
		}).
		Build()
}

// SubscribeFailed returns a standardized error for a failed configuration subscribe operation.
func (c *ConfigurationError) SubscribeFailed(keys []string, err error) error {
	msg := fmt.Sprintf("error subscribing to configuration with keys=%v from store %s: %s", keys, c.storeName, err)
	return kiterrors.NewBuilder(
		codes.InvalidArgument,
		http.StatusInternalServerError,
		msg,
		errorcodes.ConfigurationSubscribe.Code,
		string(errorcodes.ConfigurationSubscribe.Category),
	).
		WithResourceInfo(configurationStoreComponentType, c.storeName, "", msg).
		WithErrorInfo(errorcodes.ConfigurationSubscribe.GrpcCode, map[string]string{
			"store": c.storeName,
			"error": err.Error(),
		}).
		Build()
}

// UnsubscribeFailed returns a standardized error for a failed configuration unsubscribe operation.
func (c *ConfigurationError) UnsubscribeFailed(subscribeID string, err error) error {
	msg := fmt.Sprintf("error unsubscribing from configuration store %s with id %s: %s", c.storeName, subscribeID, err)
	return kiterrors.NewBuilder(
		codes.Internal,
		http.StatusInternalServerError,
		msg,
		errorcodes.ConfigurationUnsubscribe.Code,
		string(errorcodes.ConfigurationUnsubscribe.Category),
	).
		WithResourceInfo(configurationStoreComponentType, c.storeName, "", msg).
		WithErrorInfo(errorcodes.ConfigurationUnsubscribe.GrpcCode, map[string]string{
			"store":       c.storeName,
			"subscribeID": subscribeID,
			"error":       err.Error(),
		}).
		Build()
}

// UnsubscribeNotFound returns a standardized error when the subscription ID does not exist.
func (c *ConfigurationError) UnsubscribeNotFound(subscribeID string) error {
	msg := fmt.Sprintf("error unsubscribing configuration from store %s: subscription %s does not exist", c.storeName, subscribeID)
	return kiterrors.NewBuilder(
		codes.NotFound,
		http.StatusNotFound,
		msg,
		errorcodes.ConfigurationUnsubscribe.Code,
		string(errorcodes.ConfigurationUnsubscribe.Category),
	).
		WithResourceInfo(configurationStoreComponentType, c.storeName, "", msg).
		WithErrorInfo(errorcodes.ConfigurationUnsubscribe.GrpcCode, map[string]string{
			"store":       c.storeName,
			"subscribeID": subscribeID,
		}).
		Build()
}
