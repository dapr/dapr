/*
Copyright 2023 The Dapr Authors
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

package annotations

import (
	"fmt"
	"strconv"

	"github.com/dapr/dapr/utils"
)

// Map contains the annotations for the pod.
type Map map[string]string

func (a Map) GetBoolOrDefault(key string, defaultValue bool) bool {
	enabled, ok := a[key]
	if !ok {
		return defaultValue
	}
	return utils.IsTruthy(enabled)
}

func (a Map) GetStringOrDefault(key, defaultValue string) string {
	if val, ok := a[key]; ok && val != "" {
		return val
	}
	return defaultValue
}

func (a Map) GetString(key string) string {
	return a[key]
}

func (a Map) Exist(key string) bool {
	_, exist := a[key]
	return exist
}

func (a Map) GetInt32OrDefault(key string, defaultValue int32) int32 {
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

func (a Map) GetInt32(key string) (int32, error) {
	s, ok := a[key]
	if !ok {
		return -1, nil
	}
	value, err := strconv.ParseInt(s, 10, 32)
	if err != nil {
		return -1, fmt.Errorf("error parsing %s int value %s: %w", key, s, err)
	}
	return int32(value), nil
}

func New(an map[string]string) Map {
	return Map(an)
}
