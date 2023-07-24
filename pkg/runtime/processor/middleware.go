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

package processor

import (
	"context"

	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
)

// middleware is a component that implements the middleware interface. It's
// currently a no-op.
type middleware struct{}

func (m *middleware) init(_ context.Context, _ compapi.Component) error {
	return nil
}
