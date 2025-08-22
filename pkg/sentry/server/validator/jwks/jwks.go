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

package jwks

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/lestrrat-go/jwx/v2/jws"
	"github.com/lestrrat-go/jwx/v2/jwt"
	"github.com/spiffe/go-spiffe/v2/spiffeid"

	"github.com/dapr/dapr/pkg/healthz"
	sentryv1pb "github.com/dapr/dapr/pkg/proto/sentry/v1"
	"github.com/dapr/dapr/pkg/sentry/server/validator"
	"github.com/dapr/dapr/pkg/sentry/server/validator/internal"
	"github.com/dapr/kit/jwkscache"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.sentry.identity.jwks")

type Options struct {
	// SPIFFE ID of Sentry.
	SentryID spiffeid.ID `mapstructure:"-"`
	// Location of the JWKS: a URL, path on local file, or the actual JWKS (optionally base64-encoded)
	Source string `mapstructure:"source"`
	// Optional CA certificate to trust. Can be a path to a local file or an actual, PEM-encoded certificate
	CACertificate string `mapstructure:"caCertificate"`
	// Minimum interval before the JWKS can be refrehsed if fetched from a HTTP(S) endpoint.
	MinRefreshInterval time.Duration `mapstructure:"minRefreshInterval"`
	// Timeout for network requests.
	RequestTimeout time.Duration `mapstructure:"requestTimeout"`
	// Healthz controls the healthz endpoint for the JWKS cache.
	Healthz healthz.Healthz
}

// jwks implements the validator.Interface.
// It validates the request by reviewing a JWT signed by a key included in a JWKS.
// The JWT must be signed by a key included in the JWKS and must contain the following claims:
// - aud: must include the audience of Sentry (SPIFFE ID)
// - sub: must include the SPIFFE ID of the requestor
type jwks struct {
	sentryAudience string
	cache          *jwkscache.JWKSCache
	htarget        healthz.Target
}

func New(opts Options) (validator.Validator, error) {
	cache := jwkscache.NewJWKSCache(opts.Source, log)

	// Set options
	if opts.MinRefreshInterval > time.Second {
		cache.SetMinRefreshInterval(opts.MinRefreshInterval)
	}
	if opts.RequestTimeout > time.Millisecond {
		cache.SetRequestTimeout(opts.RequestTimeout)
	}
	if opts.CACertificate != "" {
		cache.SetCACertificate(opts.CACertificate)
	}

	return &jwks{
		sentryAudience: opts.SentryID.String(),
		cache:          cache,
		htarget:        opts.Healthz.AddTarget("sentry-jwks-validator"),
	}, nil
}

func (j *jwks) Start(ctx context.Context) error {
	defer j.htarget.NotReady()
	go func() {
		if err := j.cache.WaitForCacheReady(ctx); err != nil {
			return
		}
		j.htarget.Ready()
	}()

	// Start the cache. Note this is a blocking call
	err := j.cache.Start(ctx)
	if err != nil {
		return err
	}

	return nil
}

func (j *jwks) Validate(ctx context.Context, req *sentryv1pb.SignCertificateRequest) (validator.ValidateResult, error) {
	if req.GetToken() == "" {
		return validator.ValidateResult{}, errors.New("the request does not contain a token")
	}

	if err := j.cache.WaitForCacheReady(ctx); err != nil {
		return validator.ValidateResult{}, errors.New("jwks validator not ready")
	}

	// Validate the internal request
	// This also returns the trust domain.
	td, err := internal.Validate(ctx, req)
	if err != nil {
		return validator.ValidateResult{}, err
	}

	// Construct the expected value for the subject, which is the SPIFFE ID of the requestor
	sub, err := spiffeid.FromSegments(td, "ns", req.GetNamespace(), req.GetId())
	if err != nil {
		return validator.ValidateResult{}, fmt.Errorf("failed to construct SPIFFE ID for requestor: %w", err)
	}

	// Validate the authorization token
	_, err = jwt.Parse([]byte(req.GetToken()),
		jwt.WithKeySet(j.cache.KeySet(), jws.WithInferAlgorithmFromKey(true)),
		jwt.WithAcceptableSkew(5*time.Minute),
		jwt.WithContext(ctx),
		jwt.WithAudience(j.sentryAudience),
		// TODO: @joshvanl: extract the trust domain from the subject.
		jwt.WithSubject(sub.String()),
	)
	if err != nil {
		return validator.ValidateResult{}, fmt.Errorf("token validation failed: %w", err)
	}

	return validator.ValidateResult{
		TrustDomain: td,
	}, nil
}
