/*
Copyright 2022 The Dapr Authors
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

package sidecar

import (
	"strconv"

	"github.com/pkg/errors"

	"github.com/dapr/dapr/utils"
)

// Annotations contains the annotations for the container.
type Annotations map[string]string

func (a Annotations) GetBoolOrDefault(key string, defaultValue bool) bool {
	enabled, ok := a[key]
	if !ok {
		return defaultValue
	}
	return utils.IsTruthy(enabled)
}

func (a Annotations) GetStringOrDefault(key, defaultValue string) string {
	if val, ok := a[key]; ok && val != "" {
		return val
	}
	return defaultValue
}

func (a Annotations) GetString(key string) string {
	return a[key]
}

func (a Annotations) Exist(key string) bool {
	_, exist := a[key]
	return exist
}

func (a Annotations) GetInt32OrDefault(key string, defaultValue int32) int32 {
	s, ok := a[key]
	if !ok {
		return defaultValue
	}
	value, err := strconv.ParseInt(s, 10, 32)
	if err != nil {
		return defaultValue
	}
	return int32(value)
}

func (a Annotations) GetInt32(key string) (int32, error) {
	s, ok := a[key]
	if !ok {
		return -1, nil
	}
	value, err := strconv.ParseInt(s, 10, 32)
	if err != nil {
		return -1, errors.Wrapf(err, "error parsing %s int value %s ", key, s)
	}
	return int32(value), nil
}
