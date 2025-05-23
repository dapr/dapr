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

package validator

import (
	"context"

	"github.com/spiffe/go-spiffe/v2/spiffeid"

	sentryv1pb "github.com/dapr/dapr/pkg/proto/sentry/v1"
)

// ValidateResult is the result of a validation.
type ValidateResult struct {
	// TrustDomain is the trust domain of the client.
	TrustDomain spiffeid.TrustDomain
}

// Validator is used to validate the identity of a certificate requester by
// using an ID and token.
// Returns the trust domain of the certificate requester.
type Validator interface {
	// Start starts the validator.
	Start(context.Context) error

	// Validate validates the identity of a certificate request.
	// Returns the Trust Domain of the certificate requester, and whether the
	// signed certificates duration should be overridden to the maximum time
	// permitted by the singing certificate.
	Validate(context.Context, *sentryv1pb.SignCertificateRequest) (ValidateResult, error)
}
