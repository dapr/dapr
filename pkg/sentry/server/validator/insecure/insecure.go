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

	"github.com/spiffe/go-spiffe/v2/spiffeid"

	sentryv1pb "github.com/dapr/dapr/pkg/proto/sentry/v1"
	"github.com/dapr/dapr/pkg/sentry/server/validator"
	"github.com/dapr/dapr/pkg/sentry/server/validator/internal"
)

// insecure implements the validator.Interface. It doesn't perform any authentication on requests.
// It is meant to be used in self-hosted scenarios where Dapr is running on a trusted environment.
type insecure struct {
	cpTrustDomain spiffeid.TrustDomain
	cpNamespace   string
}

func New(controlPlaneTrustDomain spiffeid.TrustDomain, controlPlaneNamespace string) validator.Validator {
	return &insecure{
		cpTrustDomain: controlPlaneTrustDomain,
		cpNamespace:   controlPlaneNamespace,
	}
}

func (s *insecure) Start(ctx context.Context) error {
	<-ctx.Done()
	return nil
}

func (s *insecure) Validate(ctx context.Context, req *sentryv1pb.SignCertificateRequest) (spiffeid.TrustDomain, error) {
	return internal.Validate(ctx, s.maybeSetTrustDomain(req))
}

func (s *insecure) maybeSetTrustDomain(req *sentryv1pb.SignCertificateRequest) *sentryv1pb.SignCertificateRequest {
	newReq := &sentryv1pb.SignCertificateRequest{
		Id:                        req.Id,
		TrustDomain:               req.TrustDomain,
		Namespace:                 req.Namespace,
		Token:                     req.Token,
		CertificateSigningRequest: req.CertificateSigningRequest,
		TokenValidator:            req.TokenValidator,
	}
	if isControlPlaneService(req.Id) && req.TrustDomain == "" && req.Namespace == s.cpNamespace {
		newReq.TrustDomain = s.cpTrustDomain.String()
	}
	return newReq
}

// IsControlPlaneService returns true if the app ID corresponds to a Dapr
// control plane service.
func isControlPlaneService(id string) bool {
	switch id {
	case "dapr-operator",
		"dapr-placement",
		"dapr-injector",
		"dapr-sentry":
		return true
	default:
		return false
	}
}
