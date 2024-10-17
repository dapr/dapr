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

package localfile

import (
	"context"

	"github.com/dapr/components-contrib/secretstores"
)

type options struct {
	getSecretFn func(ctx context.Context, req secretstores.GetSecretRequest) (secretstores.GetSecretResponse, error)

	bulkGetSecretFn func(ctx context.Context, req secretstores.BulkGetSecretRequest) (secretstores.BulkGetSecretResponse, error)
}

func WithGetSecretFn(fn func(ctx context.Context, req secretstores.GetSecretRequest) (secretstores.GetSecretResponse, error)) Option {
	return func(o *options) {
		o.getSecretFn = fn
	}
}

func WithBulkGetSecretFn(fn func(ctx context.Context, req secretstores.BulkGetSecretRequest) (secretstores.BulkGetSecretResponse, error)) Option {
	return func(o *options) {
		o.bulkGetSecretFn = fn
	}
}
