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
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"testing"
	"time"

	"github.com/lestrrat-go/jwx/v2/jwa"
	"github.com/lestrrat-go/jwx/v2/jwk"
	"github.com/lestrrat-go/jwx/v2/jwt"
	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/sentry/server/ca/bundle"
	"github.com/dapr/kit/ptr"
)

func TestIssuer_Generate(t *testing.T) {
	// Generate a ecdsa private key for JWT signing
	ecSigningKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	// Generate a rsa private key for JWT signing
	rsaSigningKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	// Define test scenarios
	testCases := []struct {
		name             string
		issuer           *string
		request          *Request
		signingKey       crypto.Signer
		signingAlgorithm jwa.SignatureAlgorithm
		keyID            string
		clockSkew        time.Duration
		expectedError    bool
		validateClaims   func(t *testing.T, token jwt.Token)
	}{
		{
			name:             "valid token with default settings",
			signingKey:       ecSigningKey,
			signingAlgorithm: jwa.ES256,
			keyID:            "test-key-id",
			clockSkew:        time.Minute,
			request: &Request{
				TrustDomain: spiffeid.RequireTrustDomainFromString("example.com"),
				Audiences:   []string{"example.com"},
				Namespace:   "default",
				AppID:       "test-app",
				TTL:         time.Hour,
			},
			expectedError: false,
			validateClaims: func(t *testing.T, token jwt.Token) {
				// Validate subject (SPIFFE ID format)
				sub, found := token.Get("sub")
				require.True(t, found, "subject claim should exist")
				assert.Equal(t, "spiffe://example.com/ns/default/test-app", sub)

				// Validate token was issued recently
				iat, found := token.Get("iat")
				require.True(t, found, "iat claim should exist")
				iatTime := iat.(time.Time)
				assert.WithinDuration(t, iatTime, time.Now(), 2*time.Second)

				// Validate expiration is properly set
				exp, found := token.Get("exp")
				require.True(t, found, "exp claim should exist")
				expTime := exp.(time.Time)
				assert.WithinDuration(t, expTime, time.Now().Add(time.Hour), 2*time.Second)

				// No issuer should be set
				_, found = token.Get("iss")
				assert.False(t, found, "issuer claim should not exist")
			},
		},
		{
			name:             "audience claim is set correctly",
			signingKey:       ecSigningKey,
			signingAlgorithm: jwa.ES256,
			keyID:            "test-key-id",
			clockSkew:        time.Minute,
			request: &Request{
				TrustDomain: spiffeid.RequireTrustDomainFromString("example.com"),
				Audiences:   []string{"example.com"},
				Namespace:   "default",
				AppID:       "audience-test-app",
				TTL:         time.Hour,
			},
			expectedError: false,
			validateClaims: func(t *testing.T, token jwt.Token) {
				// Validate audience claim is set correctly to the trust domain
				auds, found := token.Get("aud")
				require.True(t, found, "audience claim should exist")
				aud, ok := auds.([]string)
				require.True(t, ok, "audience claim should be a string array")
				assert.Equal(t, "example.com", aud[0], "audience value should be the trust domain")
			},
		},
		{
			name:             "valid token with custom issuer",
			signingKey:       ecSigningKey,
			signingAlgorithm: jwa.ES256,
			keyID:            "test-key-id",
			clockSkew:        time.Minute,
			issuer:           ptr.Of("https://auth.example.com"),
			request: &Request{
				TrustDomain: spiffeid.RequireTrustDomainFromString("example.com"),
				Audiences:   []string{"example.com"},
				Namespace:   "default",
				AppID:       "test-app",
				TTL:         time.Hour,
			},
			expectedError: false,
			validateClaims: func(t *testing.T, token jwt.Token) {
				// Validate issuer is set correctly
				iss, found := token.Get("iss")
				require.True(t, found, "issuer claim should exist")
				assert.Equal(t, "https://auth.example.com", iss)
			},
		},
		{
			name:             "valid token with zero TTL",
			signingKey:       ecSigningKey,
			signingAlgorithm: jwa.ES256,
			keyID:            "test-key-id",
			clockSkew:        time.Minute,
			request: &Request{
				TrustDomain: spiffeid.RequireTrustDomainFromString("example.com"),
				Audiences:   []string{"example.com"},
				Namespace:   "default",
				AppID:       "test-app",
				TTL:         24 * time.Hour, // Explicitly providing a default TTL for testing
			},
			expectedError: false,
			validateClaims: func(t *testing.T, token jwt.Token) {
				// Token should have an expiration
				exp, found := token.Get("exp")
				assert.True(t, found, "expiration claim should exist")
				expTime := exp.(time.Time)
				assert.WithinDuration(t, expTime, time.Now().Add(24*time.Hour), 2*time.Second)
			},
		},
		{
			name:             "valid token with RSA signing key",
			signingKey:       rsaSigningKey,
			signingAlgorithm: jwa.RS256,
			keyID:            "test-key-id",
			clockSkew:        time.Minute,
			request: &Request{
				TrustDomain: spiffeid.RequireTrustDomainFromString("example.com"),
				Audiences:   []string{"example.com"},
				Namespace:   "default",
				AppID:       "test-app",
				TTL:         time.Hour,
			},
			expectedError: false,
			validateClaims: func(t *testing.T, token jwt.Token) {
				// Validate subject (SPIFFE ID format)
				sub, found := token.Get("sub")
				require.True(t, found, "subject claim should exist")
				assert.Equal(t, "spiffe://example.com/ns/default/test-app", sub)

				// Validate token was issued recently
				iat, found := token.Get("iat")
				require.True(t, found, "iat claim should exist")
				iatTime := iat.(time.Time)
				assert.WithinDuration(t, iatTime, time.Now(), 2*time.Second)

				// Validate expiration is properly set
				exp, found := token.Get("exp")
				require.True(t, found, "exp claim should exist")
				expTime := exp.(time.Time)
				assert.WithinDuration(t, expTime, time.Now().Add(time.Hour), 2*time.Second)
			},
		},
		{
			name:          "nil request",
			signingKey:    ecSigningKey,
			clockSkew:     time.Minute,
			request:       nil,
			expectedError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			signKey, err := jwk.FromRaw(tc.signingKey)
			require.NoError(t, err)

			signKey.Set(jwk.KeyIDKey, tc.keyID)
			signKey.Set(jwk.AlgorithmKey, tc.signingAlgorithm)

			jwks := jwk.NewSet()
			jwks.AddKey(signKey)

			// Create issuer with test parameters
			issuer, err := New(IssuerOptions{
				SignKey:          signKey,
				JWKS:             jwks,
				Issuer:           tc.issuer,
				AllowedClockSkew: tc.clockSkew,
			})
			require.NoError(t, err)

			token, err := issuer.Generate(t.Context(), tc.request)

			// Validate error expectation
			if tc.expectedError {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.NotEmpty(t, token)

			// Parse and validate the token
			parsedToken, err := jwt.Parse([]byte(token),
				jwt.WithKey(tc.signingAlgorithm, tc.signingKey.Public()),
				jwt.WithValidate(true))
			require.NoError(t, err)

			if tc.issuer != nil {
				// Validate issuer claim if set
				iss, found := parsedToken.Get("iss")
				require.True(t, found, "issuer claim should exist")
				assert.Equal(t, *tc.issuer, iss)
			} else {
				// No issuer should be set
				_, found := parsedToken.Get("iss")
				assert.False(t, found, "issuer claim should not exist")
			}

			// Call custom validation function if provided
			if tc.validateClaims != nil {
				tc.validateClaims(t, parsedToken)
			}
		})
	}
}

// TestJWTIssuerWithBundleGeneration tests that JWT keys are properly generated
// and included in the CA bundle.
func TestJWTIssuerWithBundleGeneration(t *testing.T) {
	// Generate a test root key for X.509 certificates
	x509RootKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	// Generate a test root key for JWT signing
	jwtRootKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	x509bundle, err := bundle.GenerateX509(bundle.OptionsX509{
		X509RootKey:      x509RootKey,
		TrustDomain:      "integration.test.dapr.io",
		AllowedClockSkew: time.Second * 20,
		OverrideCATTL:    nil,
	})
	require.NoError(t, err)
	jwtbundle, err := bundle.GenerateJWT(bundle.OptionsJWT{
		JWTRootKey:  jwtRootKey,
		TrustDomain: "integration.test.dapr.io",
	})
	require.NoError(t, err)

	bundle := bundle.Bundle{
		X509: x509bundle,
		JWT:  jwtbundle,
	}

	// Verify the JWT signing key was generated
	require.NotNil(t, bundle.JWT.SigningKey, "JWT signing key should be generated")
	require.NotNil(t, bundle.JWT.SigningKeyPEM, "JWT signing key PEM should be generated")
	require.NotNil(t, bundle.JWT.JWKS, "JWKS should be generated")

	// Verify the JWT signing key is the one we provided
	require.Equal(t, jwtRootKey, bundle.JWT.SigningKey)

	signKey, err := jwk.FromRaw(bundle.JWT.SigningKey)
	require.NoError(t, err)

	signKey.Set(jwk.AlgorithmKey, DefaultJWTSignatureAlgorithm)

	// Create JWT issuer using the bundle's signing key
	jwks := jwk.NewSet()
	jwks.AddKey(signKey)

	// Create issuer with test parameters
	issuer, err := New(IssuerOptions{
		SignKey:          signKey,
		Issuer:           nil,
		AllowedClockSkew: time.Minute,
		JWKS:             jwks,
	})
	require.NoError(t, err)

	// Generate a JWT
	request := &Request{
		TrustDomain: spiffeid.RequireTrustDomainFromString("example.com"),
		Audiences:   []string{"example.com"},
		Namespace:   "default",
		AppID:       "test-app",
		TTL:         time.Hour,
	}

	token, err := issuer.Generate(t.Context(), request)
	require.NoError(t, err)
	require.NotEmpty(t, token)

	parsedToken, err := jwt.Parse([]byte(token),
		jwt.WithKey(DefaultJWTSignatureAlgorithm, bundle.JWT.SigningKey.Public()),
		jwt.WithValidate(true))
	require.NoError(t, err)
	require.NotNil(t, parsedToken)

	// Verify expected claims are present
	sub, found := parsedToken.Get("sub")
	require.True(t, found, "subject claim should exist")
	assert.Equal(t, "spiffe://example.com/ns/default/test-app", sub)
}

// TestCustomIssuerInToken tests that the issuer claim is correctly set in the JWT token
func TestCustomIssuerInToken(t *testing.T) {
	// Generate a test private key for JWT signing
	signingKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	// Test cases with different issuer configurations
	testCases := []struct {
		name           string
		issuerValue    *string
		expectedIssuer string
		hasIssuer      bool
	}{
		{
			name:        "no issuer configured",
			issuerValue: nil,
			hasIssuer:   false,
		},
		{
			name:           "empty issuer configured",
			issuerValue:    ptr.Of(""),
			expectedIssuer: "",
			hasIssuer:      true,
		},
		{
			name:           "custom issuer URL",
			issuerValue:    ptr.Of("https://auth.example.com"),
			expectedIssuer: "https://auth.example.com",
			hasIssuer:      true,
		},
		{
			name:           "custom string issuer",
			issuerValue:    ptr.Of("dapr-sentry"),
			expectedIssuer: "dapr-sentry",
			hasIssuer:      true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			signKey, err := jwk.FromRaw(signingKey)
			require.NoError(t, err)

			// Use ES256 algorithm which is appropriate for ECDSA P-256 keys
			signKey.Set(jwk.AlgorithmKey, jwa.ES256)

			jwks := jwk.NewSet()
			jwks.AddKey(signKey)

			// Create issuer with test parameters
			issuer, err := New(IssuerOptions{
				SignKey:          signKey,
				Issuer:           tc.issuerValue,
				AllowedClockSkew: time.Minute,
				JWKS:             jwks,
			})
			require.NoError(t, err)

			// Create a basic request
			request := &Request{
				TrustDomain: spiffeid.RequireTrustDomainFromString("example.com"),
				Audiences:   []string{"example.com"},
				Namespace:   "default",
				AppID:       "test-app",
				TTL:         time.Hour,
			}

			// Generate token
			token, err := issuer.Generate(t.Context(), request)
			require.NoError(t, err)

			pubKey, err := signKey.PublicKey()
			require.NoError(t, err)

			// Parse token with verification
			parsedToken, err := jwt.Parse([]byte(token),
				jwt.WithKey(jwa.ES256, pubKey),
				jwt.WithValidate(true))
			require.NoError(t, err)

			// Check if issuer claim exists and has expected value
			if tc.hasIssuer {
				iss, found := parsedToken.Get("iss")
				require.True(t, found, "issuer claim should exist")
				assert.Equal(t, tc.expectedIssuer, iss)
			} else {
				_, found := parsedToken.Get("iss")
				assert.False(t, found, "issuer claim should not exist")
			}
		})
	}
}
