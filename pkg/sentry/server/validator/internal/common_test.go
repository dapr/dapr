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
	"testing"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/stretchr/testify/assert"

	sentryv1pb "github.com/dapr/dapr/pkg/proto/sentry/v1"
)

func TestValidate(t *testing.T) {
	tests := map[string]struct {
		req    *sentryv1pb.SignCertificateRequest
		expTD  spiffeid.TrustDomain
		expErr error
	}{
		"if all errors with app ID empty, return errors": {
			req: &sentryv1pb.SignCertificateRequest{
				Id:                        "",
				Namespace:                 "",
				CertificateSigningRequest: nil,
			},
			expTD: spiffeid.TrustDomain{},
			expErr: fmt.Errorf("invalid request: %w",
				errors.Join(
					errors.New("parameter app-id cannot be empty"),
					errors.New("CSR is required"),
					errors.New("namespace is required"),
				)),
		},
		"if all errors with app ID over 64 chars long, return errors": {
			req: &sentryv1pb.SignCertificateRequest{
				Id:                        "12345678901234567890123456789012345678901234567890123456789012345",
				Namespace:                 "",
				CertificateSigningRequest: nil,
			},
			expTD: spiffeid.TrustDomain{},
			expErr: fmt.Errorf("invalid request: %w",
				errors.Join(
					errors.New("app ID must be 64 characters or less"),
					errors.New("CSR is required"),
					errors.New("namespace is required"),
				)),
		},
		"if no errors with trust domain empty, return default trust domain": {
			req: &sentryv1pb.SignCertificateRequest{
				Id:                        "my-app-id",
				CertificateSigningRequest: []byte("csr"),
				Namespace:                 "my-namespace",
				TrustDomain:               "",
			},
			expTD:  spiffeid.RequireTrustDomainFromString("public"),
			expErr: nil,
		},
		"if no errors with trust domain not empty, return trust domain": {
			req: &sentryv1pb.SignCertificateRequest{
				Id:                        "my-app-id",
				CertificateSigningRequest: []byte("csr"),
				Namespace:                 "my-namespace",
				TrustDomain:               "example.com",
			},
			expTD:  spiffeid.RequireTrustDomainFromString("example.com"),
			expErr: nil,
		},
		"if no errors with trust domain which is invalid, return error": {
			req: &sentryv1pb.SignCertificateRequest{
				Id:                        "my-app-id",
				CertificateSigningRequest: []byte("csr"),
				Namespace:                 "my-namespace",
				TrustDomain:               "example com",
			},
			expTD:  spiffeid.TrustDomain{},
			expErr: errors.New("trust domain characters are limited to lowercase letters, numbers, dots, dashes, and underscores"),
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			td, overrideDuration, err := Validate(context.Background(), test.req)
			assert.Equal(t, test.expTD, td)
			assert.Equal(t, test.expErr, err)
			assert.False(t, overrideDuration)
		})
	}
}
