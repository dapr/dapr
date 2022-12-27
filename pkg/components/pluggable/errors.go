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

package pluggable

import (
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// MethodErrorMapping represents a simple map that maps from a grpc statuscode to a domain-level error.
type MethodErrorMapping map[codes.Code]func(status.Status) error

func (m MethodErrorMapping) Merge(other MethodErrorMapping) MethodErrorMapping {
	n := MethodErrorMapping{}

	for k, v := range m {
		n[k] = v
	}

	for k, v := range other {
		n[k] = v
	}
	return n
}

// ErrorMapping represents a simple map that maps from a method name to all grpc statuscode to a domain-level error.
type ErrorsMapping map[string]MethodErrorMapping

// NewErrorsMapping create a new error mapping for the given protoref.
func NewErrorsMapping(protoRef string, m map[string]MethodErrorMapping) ErrorsMapping {
	prefixed := ErrorsMapping{}
	for method, mapping := range m {
		prefixed[fmt.Sprintf("/%s/%s", protoRef, method)] = mapping
	}
	return prefixed
}
