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

package insecure

import (
	"context"

	sentryv1pb "github.com/dapr/dapr/pkg/proto/sentry/v1"
	"github.com/dapr/dapr/pkg/sentry/server/validator"
	"github.com/dapr/dapr/pkg/sentry/server/validator/internal"
)

// insecure implements the validator.Interface. It doesn't perform any authentication on requests.
// It is meant to be used in self-hosted scenarios where Dapr is running on a trusted environment.
type insecure struct{}

func New() validator.Validator {
	return new(insecure)
}

func (s *insecure) Start(ctx context.Context) error {
	<-ctx.Done()
	return nil
}

func (s *insecure) Validate(ctx context.Context, req *sentryv1pb.SignCertificateRequest) (validator.ValidateResult, error) {
	td, err := internal.Validate(ctx, req)
	if err != nil {
		return validator.ValidateResult{}, err
	}
	return validator.ValidateResult{
		TrustDomain: td,
	}, nil
}
