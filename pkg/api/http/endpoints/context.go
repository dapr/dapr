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

package endpoints

import (
	"fmt"
)

// EndpointCtxKey is the key for storing endpoint information in the context of a request.
type EndpointCtxKey struct{}

// EndpointCtxData is the type for endpoint data stored in a request's context.
type EndpointCtxData struct {
	Group    *EndpointGroup
	Settings EndpointSettings
	SpanData any
}

// GetEndpointName returns the Settings.Name property, with nil-checks.
func (c *EndpointCtxData) GetEndpointName() string {
	if c == nil {
		return ""
	}
	return c.Settings.Name
}

// String implements fmt.Stringer and is used for debugging.
func (c *EndpointCtxData) String() string {
	if c == nil {
		return "nil"
	}
	return fmt.Sprintf("settings='%#v' group='%#v'", c.Settings, c.Group)
}
