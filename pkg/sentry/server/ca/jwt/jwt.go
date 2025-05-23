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

package jwt

import (
	"context"
	"crypto"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/lestrrat-go/jwx/v2/jwa"
	"github.com/lestrrat-go/jwx/v2/jwk"
	"github.com/lestrrat-go/jwx/v2/jwt"
	"github.com/spiffe/go-spiffe/v2/spiffeid"

	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.sentry.ca.jwt")

const (
	// DefaultKeyThumbprintAlgorithm
	DefaultKeyThumbprintAlgorithm = crypto.SHA256
	// DefaultJWTSignatureAlgorithm is set to RS256 by default as it is the most compatible algorithm.
	DefaultJWTSignatureAlgorithm = jwa.RS256
)

// Request is the request for generating a JWT
type Request struct {
	// Trust domain is the trust domain of the JWT.
	TrustDomain spiffeid.TrustDomain

	// Audiences is the audience of the JWT.
	Audiences []string

	// Namespace is the namespace of the client.
	Namespace string

	// AppID is the app id of the client.
	AppID string

	// TTL is the time-to-live for the token in seconds
	TTL time.Duration
}

type Issuer interface {
	// Generate creates a JWT token for the given request. The token includes
	// claims based on the identity information provided in the request.
	Generate(context.Context, *Request) (string, error)

	// JWKS returns the JSON Web Key Set (JWKS).
	JWKS() jwk.Set

	// JWTSignatureAlgorithm returns the signature algorithm used for signing JWTs.
	JWTSignatureAlgorithm() jwa.KeyAlgorithm
}

type issuer struct {
	// signKey is the key used to sign the JWT
	signKey jwk.Key
	// iss is the iss of the JWT (optional)
	iss *string
	// allowedClockSkew is the time allowed for clock skew
	allowedClockSkew time.Duration
	// jwks is the JSON Web Key Set (JWKS) used to verify JWTs
	jwks jwk.Set
}

type IssuerOptions struct {
	// SignKey is the key used to sign the JWT
	SignKey jwk.Key
	// Issuer is the Issuer of the JWT (optional)
	Issuer *string
	// AllowedClockSkew is the time allowed for clock skew
	AllowedClockSkew time.Duration
	// JWKS is the JSON Web Key Set (JWKS) used to verify JWTs
	JWKS jwk.Set
}

func New(opts IssuerOptions) (Issuer, error) {
	return &issuer{
		signKey:          opts.SignKey,
		iss:              opts.Issuer,
		allowedClockSkew: opts.AllowedClockSkew,
		jwks:             opts.JWKS,
	}, nil
}

// Generate creates a JWT for the given request. The JWT is always generated and included
// in the response to SignCertificate requests, without requiring additional client configuration.
// This provides a convenient alternative authentication method alongside X.509 certificates.
func (i *issuer) Generate(ctx context.Context, req *Request) (string, error) {
	if req == nil {
		return "", errors.New("request cannot be nil")
	}
	if req.TrustDomain.IsZero() {
		return "", errors.New("trust domain cannot be empty")
	}
	if len(req.Audiences) == 0 {
		return "", errors.New("audience cannot be empty")
	}
	if req.Namespace == "" {
		return "", errors.New("namespace cannot be empty")
	}
	if req.AppID == "" {
		return "", errors.New("app ID cannot be empty")
	}
	if i.signKey == nil {
		return "", errors.New("JWT signing key is not available")
	}

	// Create SPIFFE ID format string for the subject claim
	subject, err := spiffeid.FromSegments(req.TrustDomain, "ns", req.Namespace, req.AppID)
	if err != nil {
		return "", fmt.Errorf("error creating SPIFFE ID: %w", err)
	}

	now := time.Now()
	notBefore := now.Add(-i.allowedClockSkew) // Account for clock skew
	notAfter := now.Add(req.TTL)

	jti, err := generateJwtID()
	if err != nil {
		log.Errorf("Error generating nonce: %v", err)
		return "", fmt.Errorf("error generating nonce: %w", err)
	}

	// Create JWT token with claims builder
	builder := jwt.NewBuilder().
		JwtID(jti).
		Subject(subject.String()).
		IssuedAt(now).
		Audience(req.Audiences).
		NotBefore(notBefore).
		Claim("use", "sig"). // Needed for Azure
		Expiration(notAfter)

	// Set issuer only if configured
	if i.iss != nil {
		builder = builder.Issuer(*i.iss)
	}

	// Build the token
	token, err := builder.Build()
	if err != nil {
		log.Errorf("Error creating JWT token: %v", err)
		return "", fmt.Errorf("error creating JWT token: %w", err)
	}

	signedToken, err := jwt.Sign(token, jwt.WithKey(i.signKey.Algorithm(), i.signKey))
	if err != nil {
		log.Errorf("Error signing JWT token: %v", err)
		return "", fmt.Errorf("error signing JWT token: %w", err)
	}

	return string(signedToken), nil
}

func (i *issuer) JWKS() jwk.Set {
	return i.jwks
}

func (i *issuer) JWTSignatureAlgorithm() jwa.KeyAlgorithm {
	if i.signKey == nil {
		return nil
	}
	return i.signKey.Algorithm()
}

// generateJwtID creates a random JWT ID (jti) using a cryptographically secure random number generator.
func generateJwtID() (string, error) {
	nonceBytes := make([]byte, 16) // 16 bytes = 128 bits
	if _, err := rand.Read(nonceBytes); err != nil {
		return "", fmt.Errorf("failed to generate random nonce: %w", err)
	}
	return hex.EncodeToString(nonceBytes), nil
}
