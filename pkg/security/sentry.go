/*
Copyright 2025 The Dapr Authors
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

package security

import (
	"context"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"time"

	middleware "github.com/grpc-ecosystem/go-grpc-middleware"
	retry "github.com/grpc-ecosystem/go-grpc-middleware/retry"
	"github.com/lestrrat-go/jwx/v2/jwt"
	"github.com/spiffe/go-spiffe/v2/spiffegrpc/grpccredentials"
	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/spiffe/go-spiffe/v2/spiffetls/tlsconfig"
	"google.golang.org/grpc"

	"github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/modes"
	sentryv1pb "github.com/dapr/dapr/pkg/proto/sentry/v1"
	sentryToken "github.com/dapr/dapr/pkg/security/token"
	cryptopem "github.com/dapr/kit/crypto/pem"
	"github.com/dapr/kit/crypto/spiffe"
	"github.com/dapr/kit/crypto/spiffe/trustanchors"
	"github.com/dapr/kit/ptr"
)

const (
	sentrySignTimeout = time.Second * 3
	sentryMaxRetries  = 5
)

func newRequestFn(opts Options, trustAnchors trustanchors.Interface, cptd spiffeid.TrustDomain) (spiffe.RequestSVIDFn, error) {
	sentryID, err := SentryID(cptd, opts.ControlPlaneNamespace)
	if err != nil {
		return nil, err
	}

	var trustDomain *string
	ns := CurrentNamespace()

	// If the service is a control plane service, set the trust domain to the
	// control plane trust domain.
	if isControlPlaneService(opts.AppID) && opts.ControlPlaneNamespace == ns {
		trustDomain = &opts.ControlPlaneTrustDomain
	}

	// return injected identity, default id if not present
	sentryIdentifier := os.Getenv("SENTRY_LOCAL_IDENTITY")
	if sentryIdentifier == "" {
		sentryIdentifier = opts.AppID
	}

	sentryAddress := opts.SentryAddress
	sentryTokenFile := opts.SentryTokenFile
	kubernetesMode := opts.Mode == modes.KubernetesMode

	fn := func(ctx context.Context, csrDER []byte) (*spiffe.SVIDResponse, error) {
		unaryClientInterceptor := retry.UnaryClientInterceptor(
			retry.WithMax(sentryMaxRetries),
			retry.WithPerRetryTimeout(sentrySignTimeout),
		)
		if diagnostics.DefaultGRPCMonitoring.IsEnabled() {
			unaryClientInterceptor = middleware.ChainUnaryClient(
				unaryClientInterceptor,
				diagnostics.DefaultGRPCMonitoring.UnaryClientInterceptor(),
			)
		}

		conn, err := grpc.DialContext(ctx, //nolint:staticcheck
			sentryAddress,
			grpc.WithTransportCredentials(
				grpccredentials.TLSClientCredentials(trustAnchors, tlsconfig.AuthorizeID(sentryID)),
			),
			grpc.WithUnaryInterceptor(unaryClientInterceptor),
			grpc.WithReturnConnectionError(), //nolint:staticcheck
		)
		if err != nil {
			diagnostics.DefaultMonitoring.MTLSWorkLoadCertRotationFailed("sentry_conn")
			return nil, fmt.Errorf("error establishing connection to sentry: %w", err)
		}

		defer conn.Close()

		var token string
		var tokenValidator sentryv1pb.SignCertificateRequest_TokenValidator
		if sentryTokenFile != nil {
			token, tokenValidator, err = sentryToken.GetSentryTokenFromFile(*sentryTokenFile)
		} else {
			token, tokenValidator, err = sentryToken.GetSentryToken(kubernetesMode)
		}

		if err != nil {
			diagnostics.DefaultMonitoring.MTLSWorkLoadCertRotationFailed("sentry_token")
			return nil, fmt.Errorf("error obtaining token: %w", err)
		}

		req := &sentryv1pb.SignCertificateRequest{
			CertificateSigningRequest: pem.EncodeToMemory(&pem.Block{
				Type: "CERTIFICATE REQUEST", Bytes: csrDER,
			}),
			Id:             sentryIdentifier,
			Token:          token,
			Namespace:      ns,
			TokenValidator: tokenValidator,
			JwtAudiences:   opts.JwtAudiences,
		}

		if trustDomain != nil {
			req.TrustDomain = *trustDomain
		}

		resp, err := sentryv1pb.NewCAClient(conn).SignCertificate(ctx, req)
		if err != nil {
			diagnostics.DefaultMonitoring.MTLSWorkLoadCertRotationFailed("sign")
			return nil, fmt.Errorf("error from sentry SignCertificate: %w", err)
		}

		if err = resp.GetValidUntil().CheckValid(); err != nil {
			diagnostics.DefaultMonitoring.MTLSWorkLoadCertRotationFailed("invalid_ts")
			return nil, fmt.Errorf("error parsing ValidUntil: %w", err)
		}

		workloadcert, err := cryptopem.DecodePEMCertificates(resp.GetWorkloadCertificate())
		if err != nil {
			return nil, fmt.Errorf("error parsing newly signed certificate: %w", err)
		}

		var jwtVal *string
		if resp.GetJwt() != nil && resp.GetJwt().GetValue() != "" {
			// NOTE: We do not verify the signature of the token here
			// as the token was passed over a secure channel. This avoids
			// potential issues where the workload does not yet have an
			// authority used by Sentry but that would be successfully
			// validated by a 3rd party via the OIDC server.
			tkn, err := jwt.Parse([]byte(resp.GetJwt().GetValue()),
				jwt.WithAcceptableSkew(5*time.Minute),
				jwt.WithContext(ctx),
				jwt.WithVerify(false))
			if err != nil {
				diagnostics.DefaultMonitoring.MTLSWorkLoadCertRotationFailed("jwt_parse")
				return nil, fmt.Errorf("error parsing JWT: %w", err)
			}

			if len(tkn.Audience()) == 0 {
				diagnostics.DefaultMonitoring.MTLSWorkLoadCertRotationFailed("jwt_aud")
				return nil, errors.New("JWT audience is empty")
			}

			jwtVal = ptr.Of(resp.GetJwt().GetValue())
		}

		return &spiffe.SVIDResponse{
			X509Certificates: workloadcert,
			JWT:              jwtVal,
		}, nil
	}

	return fn, nil
}

// isControlPlaneService returns true if the app ID corresponds to a Dapr
// control plane service.
func isControlPlaneService(id string) bool {
	switch id {
	case "dapr-operator",
		"dapr-placement",
		"dapr-injector",
		"dapr-sentry",
		"dapr-scheduler":
		return true
	default:
		return false
	}
}
