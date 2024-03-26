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

package sentry

import (
	"context"

	sentryv1pb "github.com/dapr/dapr/pkg/proto/sentry/v1"
)

type options struct {
	signCertificateFn func(context.Context, *sentryv1pb.SignCertificateRequest) (*sentryv1pb.SignCertificateResponse, error)
}

func WithSignCertificateFn(fn func(context.Context, *sentryv1pb.SignCertificateRequest) (*sentryv1pb.SignCertificateResponse, error)) func(*options) {
	return func(o *options) {
		o.signCertificateFn = fn
	}
}
