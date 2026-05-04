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
	"strings"

	"google.golang.org/grpc/codes"

	"github.com/dapr/components-contrib/metadata"
	"github.com/dapr/dapr/pkg/messages/errorcodes"
	"github.com/dapr/kit/errors"
)

type ConfigurationStoreError struct {
	name             string
	skipResourceInfo bool
}

func ConfigurationStore(name string) *ConfigurationStoreError {
	return &ConfigurationStoreError{name: name}
}

func (c *ConfigurationStoreError) NotFound(appID string) error {
	msg := fmt.Sprintf("%s store %s is not found", metadata.ConfigurationStoreType, c.name)
	c.skipResourceInfo = true
	var meta map[string]string
	if len(appID) > 0 {
		meta = map[string]string{"appID": appID}
	}
	return c.build(
		errors.NewBuilder(
			codes.InvalidArgument,
			http.StatusBadRequest,
			msg,
			errorcodes.ConfigurationStoreNotFound.Code,
			string(errorcodes.ConfigurationStoreNotFound.Category),
		),
		errorcodes.ConfigurationStoreNotFound.GrpcCode,
		meta,
	)
}

func (c *ConfigurationStoreError) NotConfigured(appID string) error {
	msg := fmt.Sprintf("%s stores not configured", metadata.ConfigurationStoreType)
	c.skipResourceInfo = true
	var meta map[string]string
	if len(appID) > 0 {
		meta = map[string]string{"appID": appID}
	}
	return c.build(
		errors.NewBuilder(
			codes.FailedPrecondition,
			http.StatusInternalServerError,
			msg,
			errorcodes.ConfigurationStoreNotConfigured.Code,
			string(errorcodes.ConfigurationStoreNotConfigured.Category),
		),
		errorcodes.ConfigurationStoreNotConfigured.GrpcCode,
		meta,
	)
}

func (c *ConfigurationStoreError) GetFailed(keys []string, err error) error {
	return c.build(
		errors.NewBuilder(
			codes.Internal,
			http.StatusInternalServerError,
			fmt.Sprintf("failed to get %s from %s store %s: %v", strings.Join(keys, ","), metadata.ConfigurationStoreType, c.name, err),
			errorcodes.ConfigurationGet.Code,
			string(errorcodes.ConfigurationGet.Category),
		),
		errorcodes.ConfigurationGet.GrpcCode,
		nil,
	)
}

func (c *ConfigurationStoreError) SubscribeFailed(keys []string, err error) error {
	return c.build(
		errors.NewBuilder(
			codes.InvalidArgument,
			http.StatusInternalServerError,
			fmt.Sprintf("failed to subscribe %s from %s store %s: %v", strings.Join(keys, ","), metadata.ConfigurationStoreType, c.name, err),
			errorcodes.ConfigurationSubscribe.Code,
			string(errorcodes.ConfigurationSubscribe.Category),
		),
		errorcodes.ConfigurationSubscribe.GrpcCode,
		nil,
	)
}

func (c *ConfigurationStoreError) UnsubscribeFailed(id string, err error) error {
	return c.build(
		errors.NewBuilder(
			codes.Internal,
			http.StatusInternalServerError,
			fmt.Sprintf("failed to unsubscribe to configuration request %s: %v", id, err),
			errorcodes.ConfigurationUnsubscribe.Code,
			string(errorcodes.ConfigurationUnsubscribe.Category),
		),
		errorcodes.ConfigurationUnsubscribe.GrpcCode,
		nil,
	)
}

func (c *ConfigurationStoreError) build(err *errors.ErrorBuilder, errCode string, meta map[string]string) error {
	if !c.skipResourceInfo {
		err = err.WithResourceInfo(string(metadata.ConfigurationStoreType), c.name, "", "")
	}
	return err.
		WithErrorInfo(errCode, meta).
		Build()
}
