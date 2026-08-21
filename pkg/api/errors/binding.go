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

const bindingComponentType = "binding"

// BindingError builds rich errors for the Bindings API.
type BindingError struct {
	name string
}

// Binding returns a BindingError scoped to the given output binding name.
func Binding(name string) *BindingError {
	return &BindingError{name: name}
}

// InvokeOutputBinding returns a standardized error for a failed output-binding invocation.
func (b *BindingError) InvokeOutputBinding(err error) error {
	msg := fmt.Sprintf("error invoking output binding %s: %s", b.name, err)
	return kiterrors.NewBuilder(
		codes.Internal,
		http.StatusInternalServerError,
		msg,
		errorcodes.BindingInvokeOutputBinding.Code,
		string(errorcodes.BindingInvokeOutputBinding.Category),
	).
		WithResourceInfo(bindingComponentType, b.name, "", msg).
		WithErrorInfo(
			errorcodes.BindingInvokeOutputBinding.GrpcCode,
			map[string]string{
				"name":  b.name,
				"error": err.Error(),
			},
		).
		Build()
}
