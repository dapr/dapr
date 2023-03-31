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

package internal

import (
	"context"
	"errors"
	"fmt"

	"github.com/dapr/kit/logger"
	"github.com/spiffe/go-spiffe/v2/spiffeid"

	sentryv1pb "github.com/dapr/dapr/pkg/proto/sentry/v1"
)

var log = logger.NewLogger("dapr.sentry.identity.common")

// Validate validates the common rules for all requests.
func Validate(_ context.Context, req *sentryv1pb.SignCertificateRequest) (spiffeid.TrustDomain, error) {
	var errs []error

	// TODO: we should also validate that the app ID is alpha numeric, and
	// doesn't contain things like spaces etc.
	if len(req.Id) == 0 {
		errs = append(errs, errors.New("app ID is required"))
	}
	if len(req.Id) > 64 {
		errs = append(errs, errors.New("app ID must be 64 characters or less"))
	}

	if len(req.CertificateSigningRequest) == 0 {
		errs = append(errs, errors.New("CSR is required"))
	}

	if len(req.Namespace) == 0 {
		errs = append(errs, errors.New("namespace is required"))
	}

	if len(errs) > 0 {
		return spiffeid.TrustDomain{}, fmt.Errorf("invalid request: %w", errors.Join(errs...))
	}

	if len(req.GetTrustDomain()) == 0 {
		// Default to public trust domain if not specified.
		return spiffeid.RequireTrustDomainFromString("public"), nil
	}

	return spiffeid.TrustDomainFromString(req.GetTrustDomain())
}
