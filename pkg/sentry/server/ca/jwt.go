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

package ca

import (
	"context"
	"fmt"
	"time"

	"github.com/lestrrat-go/jwx/v2/jwa"
	"github.com/lestrrat-go/jwx/v2/jwt"
)

// JWTRequest is the request for generating a JWT
type JWTRequest struct {
	// TrustDomain is the trust domain of the client.
	TrustDomain string

	// Namespace is the namespace of the client.
	Namespace string

	// AppID is the app id of the client.
	AppID string

	// TTL is the time-to-live for the token in seconds
	// If 0, defaults to the configured workload certificate TTL
	TTL time.Duration
}

// GenerateJWT creates a JWT for the given request. The JWT is always generated and included
// in the response to SignCertificate requests, without requiring additional client configuration.
// This provides a convenient alternative authentication method alongside X.509 certificates.
func (c *ca) GenerateJWT(ctx context.Context, req *JWTRequest) (string, error) {
	if req == nil {
		return "", fmt.Errorf("request cannot be nil")
	}

	ttl := req.TTL
	if ttl == 0 {
		ttl = c.config.WorkloadCertTTL
	}

	// Create SPIFFE ID format string for the subject claim
	subject := fmt.Sprintf("spiffe://%s/ns/%s/%s", req.TrustDomain, req.Namespace, req.AppID)

	now := time.Now()
	notBefore := now.Add(-c.config.AllowedClockSkew) // Account for clock skew
	notAfter := now.Add(ttl)

	// Create JWT token with claims
	token, err := jwt.NewBuilder().
		Subject(subject).
		IssuedAt(now).
		Issuer(c.config.TrustDomain).
		NotBefore(notBefore).
		Expiration(notAfter).
		Build()
	if err != nil {
		log.Errorf("Error creating JWT token: %v", err)
		return "", fmt.Errorf("error creating JWT token: %w", err)
	}

	// Choose the signing key - prefer the dedicated JWT signing key if available
	var signingKey interface{} = c.bundle.IssKey // Fallback to issuer key
	var signingAlg jwa.SignatureAlgorithm = jwa.ES256

	if c.bundle.JWTSigningKey != nil {
		signingKey = c.bundle.JWTSigningKey
		log.Debugf("Using dedicated JWT signing key for token: %s", subject)
	} else {
		log.Debugf("Using issuer key for signing JWT token (dedicated JWT key not available): %s", subject)
	}

	// Sign the token with the chosen key
	signedToken, err := jwt.Sign(token, jwt.WithKey(signingAlg, signingKey))
	if err != nil {
		log.Errorf("Error signing JWT token: %v", err)
		return "", fmt.Errorf("error signing JWT token: %w", err)
	}

	return string(signedToken), nil
}
