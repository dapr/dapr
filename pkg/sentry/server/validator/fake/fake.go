//go:build unit
// +build unit

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

package fake

import (
	"context"

	sentryv1pb "github.com/dapr/dapr/pkg/proto/sentry/v1"
	"github.com/dapr/dapr/pkg/sentry/server/validator"
)

// Fake implements the validator.Interface. It is used in tests.
type Fake struct {
	validateFn func(context.Context, *sentryv1pb.SignCertificateRequest) (validator.ValidateResult, error)
	startFn    func(context.Context) error
}

func New() *Fake {
	return &Fake{
		startFn: func(context.Context) error {
			return nil
		},
		validateFn: func(ctx context.Context, req *sentryv1pb.SignCertificateRequest) (validator.ValidateResult, error) {
			return validator.ValidateResult{}, nil
		},
	}
}

func (f *Fake) WithValidateFn(fn func(ctx context.Context, req *sentryv1pb.SignCertificateRequest) (validator.ValidateResult, error)) *Fake {
	f.validateFn = fn
	return f
}

func (f *Fake) WithStartFn(fn func(ctx context.Context) error) *Fake {
	f.startFn = fn
	return f
}

func (f *Fake) Validate(ctx context.Context, req *sentryv1pb.SignCertificateRequest) (validator.ValidateResult, error) {
	return f.validateFn(ctx, req)
}

func (f *Fake) Start(ctx context.Context) error {
	return f.startFn(ctx)
}
