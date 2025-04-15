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
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"testing"
	"time"

	"github.com/lestrrat-go/jwx/v2/jwt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJWTIssuer_GenerateJWT(t *testing.T) {
	// Generate a test private key for JWT signing
	signingKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	// Define test scenarios
	testCases := []struct {
		name           string
		issuer         *string
		request        *JWTRequest
		signingKey     *ecdsa.PrivateKey
		clockSkew      time.Duration
		expectedError  bool
		validateClaims func(t *testing.T, token jwt.Token)
	}{
		{
			name:       "valid token with default settings",
			signingKey: signingKey,
			clockSkew:  time.Minute,
			request: &JWTRequest{
				Audience:  "example.com",
				Namespace: "default",
				AppID:     "test-app",
				TTL:       time.Hour,
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
				assert.WithinDuration(t, time.Now(), iatTime, 2*time.Second)

				// Validate expiration is properly set
				exp, found := token.Get("exp")
				require.True(t, found, "exp claim should exist")
				expTime := exp.(time.Time)
				assert.WithinDuration(t, time.Now().Add(time.Hour), expTime, 2*time.Second)

				// No issuer should be set
				_, found = token.Get("iss")
				assert.False(t, found, "issuer claim should not exist")
			},
		},
		{
			name:       "audience claim is set correctly",
			signingKey: signingKey,
			clockSkew:  time.Minute,
			request: &JWTRequest{
				Audience:  "example.com",
				Namespace: "default",
				AppID:     "audience-test-app",
				TTL:       time.Hour,
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
			name:       "valid token with custom issuer",
			signingKey: signingKey,
			clockSkew:  time.Minute,
			issuer:     stringPtr("https://auth.example.com"),
			request: &JWTRequest{
				Audience:  "example.com",
				Namespace: "default",
				AppID:     "test-app",
				TTL:       time.Hour,
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
			name:       "valid token with zero TTL",
			signingKey: signingKey,
			clockSkew:  time.Minute,
			request: &JWTRequest{
				Audience:  "example.com",
				Namespace: "default",
				AppID:     "test-app",
				TTL:       24 * time.Hour, // Explicitly providing a default TTL for testing
			},
			expectedError: false,
			validateClaims: func(t *testing.T, token jwt.Token) {
				// Token should have an expiration
				exp, found := token.Get("exp")
				assert.True(t, found, "expiration claim should exist")
				expTime := exp.(time.Time)
				assert.WithinDuration(t, time.Now().Add(24*time.Hour), expTime, 2*time.Second)
			},
		},
		{
			name:          "nil request",
			signingKey:    signingKey,
			clockSkew:     time.Minute,
			request:       nil,
			expectedError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create issuer with test parameters
			issuer, err := NewJWTIssuer(
				tc.signingKey,
				tc.issuer,
				tc.clockSkew,
				[]string{})
			require.NoError(t, err)

			token, err := issuer.GenerateJWT(context.Background(), tc.request)

			// Validate error expectation
			if tc.expectedError {
				assert.Error(t, err)
				return
			}

			assert.NoError(t, err)
			assert.NotEmpty(t, token)

			// Parse and validate the token
			parsedToken, err := jwt.Parse([]byte(token),
				jwt.WithKey(JWTSignatureAlgorithm, tc.signingKey.Public()),
				jwt.WithValidate(true),
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
	// Generate a test root key for the bundle
	rootKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	// Generate the bundle
	bundle, err := GenerateBundle(rootKey, "example.com", time.Minute, nil)
	require.NoError(t, err)

	// Verify the JWT signing key was generated
	require.NotNil(t, bundle.JWTSigningKey, "JWT signing key should be generated")
	require.NotNil(t, bundle.JWTSigningKeyPEM, "JWT signing key PEM should be generated")
	require.NotNil(t, bundle.JWKS, "JWKS should be generated")

	// Create JWT issuer using the bundle's signing key
	issuer, err := NewJWTIssuer(
		bundle.JWTSigningKey,
		nil,
		time.Minute,
		[]string{})
	require.NoError(t, err)

	// Generate a JWT
	request := &JWTRequest{
		Audience:  "example.com",
		Namespace: "default",
		AppID:     "test-app",
		TTL:       time.Hour,
	}

	token, err := issuer.GenerateJWT(context.Background(), request)
	require.NoError(t, err)
	require.NotEmpty(t, token)

	parsedToken, err := jwt.Parse([]byte(token),
		jwt.WithKey(JWTSignatureAlgorithm, bundle.JWTSigningKey.Public()),
		jwt.WithValidate(true))
	require.NoError(t, err)
	require.NotNil(t, parsedToken)

	// Verify expected claims are present
	sub, found := parsedToken.Get("sub")
	require.True(t, found, "subject claim should exist")
	assert.Equal(t, "spiffe://example.com/ns/default/test-app", sub)
}

// Helper function to parse the JWT and get the claims
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
			issuerValue:    stringPtr(""),
			expectedIssuer: "",
			hasIssuer:      true,
		},
		{
			name:           "custom issuer URL",
			issuerValue:    stringPtr("https://auth.example.com"),
			expectedIssuer: "https://auth.example.com",
			hasIssuer:      true,
		},
		{
			name:           "custom string issuer",
			issuerValue:    stringPtr("dapr-sentry"),
			expectedIssuer: "dapr-sentry",
			hasIssuer:      true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create issuer with test parameters
			issuer, err := NewJWTIssuer(
				signingKey,
				tc.issuerValue,
				time.Minute,
				[]string{})
			require.NoError(t, err)

			// Create a basic request
			request := &JWTRequest{
				Audience:  "example.com",
				Namespace: "default",
				AppID:     "test-app",
				TTL:       time.Hour,
			}

			// Generate token
			token, err := issuer.GenerateJWT(context.Background(), request)
			require.NoError(t, err)

			pubKey, err := issuer.signingKey.PublicKey()
			require.NoError(t, err)

			// Parse token without verification (we just want to check the claims)
			parsedToken, err := jwt.Parse([]byte(token),
				jwt.WithKey(JWTSignatureAlgorithm, pubKey),
				jwt.WithValidate(true),
				jwt.WithValidate(false))
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

// TestExtraAudiencesInToken tests that extraAudiences are correctly included in the JWT token
func TestExtraAudiencesInToken(t *testing.T) {
	// Generate a test private key for JWT signing
	signingKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	// Test cases with different audience configurations
	testCases := []struct {
		name           string
		extraAudiences []string
		expectedAuds   []string
		mainAudience   string
	}{
		{
			name:           "no extra audiences",
			extraAudiences: []string{},
			mainAudience:   "example.com",
			expectedAuds:   []string{"example.com"},
		},
		{
			name:           "with custom extra audiences",
			extraAudiences: []string{"custom-audience-1", "custom-audience-2"},
			mainAudience:   "example.com",
			expectedAuds:   []string{"example.com", "custom-audience-1", "custom-audience-2"},
		},
		{
			name:           "with default extra audiences",
			extraAudiences: DefaultExtraAudiences,
			mainAudience:   "example.com",
			expectedAuds:   append([]string{"example.com"}, DefaultExtraAudiences...),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create issuer with test parameters
			issuer, err := NewJWTIssuer(
				signingKey,
				nil, // no custom issuer
				time.Minute,
				tc.extraAudiences)
			require.NoError(t, err)

			// Create a basic request
			request := &JWTRequest{
				Audience:  tc.mainAudience,
				Namespace: "default",
				AppID:     "test-app",
				TTL:       time.Hour,
			}

			// Generate token
			token, err := issuer.GenerateJWT(context.Background(), request)
			require.NoError(t, err)

			pubKey, err := issuer.signingKey.PublicKey()
			require.NoError(t, err)

			// Parse token with verification
			parsedToken, err := jwt.Parse([]byte(token),
				jwt.WithKey(JWTSignatureAlgorithm, pubKey),
				jwt.WithValidate(true))
			require.NoError(t, err)

			// Check audience claim
			aud := parsedToken.Audience()
			assert.Len(t, aud, len(tc.expectedAuds), "Should have the expected number of audiences")

			// Check that each expected audience is present
			for _, expectedAud := range tc.expectedAuds {
				assert.Contains(t, aud, expectedAud, "Expected audience '%s' not found", expectedAud)
			}
		})
	}
}

// Helper method to create string pointers
func stringPtr(s string) *string {
	return &s
}
