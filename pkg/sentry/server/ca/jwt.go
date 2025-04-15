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
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"fmt"
	"time"

	"github.com/lestrrat-go/jwx/v2/jwa"
	"github.com/lestrrat-go/jwx/v2/jwk"
	"github.com/lestrrat-go/jwx/v2/jwt"
)

const (
	// for both the provided or generated signing keys,
	// if "kid" not set, we will set it to this value.
	DefaultJWTKeyID              = "dapr-sentry"
	DefaultJWTSignatureAlgorithm = jwa.ES256
)

// By defaults we include common cloud audiences in the
// token so that components can request the token without
// needing to know the trust domain which is the required
// audience.
// Users can override these defaults via config.
var DefaultExtraAudiences = []string{
	"api://AzureADTokenExchange",
	"sts.amazonaws.com",
	"iam.googleapis.com",
}

// JWTRequest is the request for generating a JWT
type JWTRequest struct {
	// Audience is the audience of the JWT.
	Audience string

	// Namespace is the namespace of the client.
	Namespace string

	// AppID is the app id of the client.
	AppID string

	// TTL is the time-to-live for the token in seconds
	TTL time.Duration
}

type jwtIssuer struct {
	// signingKey is the key used to sign the JWT
	signingKey jwk.Key

	// issuer is the issuer of the JWT (optional)
	issuer *string

	// allowedClockSkew is the time allowed for clock skew
	allowedClockSkew time.Duration

	// extraAudiences are optional audiences to include in the JWT
	extraAudiences []string
}

func NewJWTIssuer(signingKey crypto.Signer, issuer *string, allowedClockSkew time.Duration, extraAudiences []string) (jwtIssuer, error) {
	if signingKey == nil {
		return jwtIssuer{}, fmt.Errorf("signing key cannot be nil")
	}

	sk, err := jwk.FromRaw(signingKey)
	if err != nil {
		return jwtIssuer{}, fmt.Errorf("failed to create JWK from signing key: %w", err)
	}

	// jwk.FromRaw does not set the algorithm, so we
	// need to set it manually if it is not already set.
	if sk.Algorithm().String() == "" {
		alg, err := signatureAlgorithmFrom(signingKey)
		if err != nil {
			return jwtIssuer{}, fmt.Errorf("failed to determine signature algorithm: %w", err)
		}

		log.Debugf("Setting jwt issuer algorithm to %s", alg)
		if err := sk.Set(jwk.AlgorithmKey, alg.String()); err != nil {
			return jwtIssuer{}, fmt.Errorf("failed to set algorithm: %w", err)
		}
	} else {
		log.Debugf("Using provided jwt issuer algorithm %s", sk.Algorithm())
	}

	if sk.KeyID() == "" {
		log.Debugf("Setting jwt issuer key id to %s", DefaultJWTKeyID)
		if err := sk.Set(jwk.KeyIDKey, DefaultJWTKeyID); err != nil {
			return jwtIssuer{}, fmt.Errorf("failed to set key ID: %w", err)
		}
	}

	return jwtIssuer{
		signingKey:       sk,
		issuer:           issuer,
		allowedClockSkew: allowedClockSkew,
		extraAudiences:   extraAudiences,
	}, nil
}

// GenerateJWT creates a JWT for the given request. The JWT is always generated and included
// in the response to SignCertificate requests, without requiring additional client configuration.
// This provides a convenient alternative authentication method alongside X.509 certificates.
func (i *jwtIssuer) GenerateJWT(ctx context.Context, req *JWTRequest) (string, error) {
	if req == nil {
		return "", fmt.Errorf("request cannot be nil")
	}
	if req.Audience == "" {
		return "", fmt.Errorf("audience cannot be empty")
	}
	if req.Namespace == "" {
		return "", fmt.Errorf("namespace cannot be empty")
	}
	if req.AppID == "" {
		return "", fmt.Errorf("app ID cannot be empty")
	}
	if i.signingKey == nil {
		return "", fmt.Errorf("JWT signing key is not available")
	}

	// Create SPIFFE ID format string for the subject claim
	subject := fmt.Sprintf("spiffe://%s/ns/%s/%s", req.Audience, req.Namespace, req.AppID)

	now := time.Now()
	notBefore := now.Add(-i.allowedClockSkew) // Account for clock skew
	notAfter := now.Add(req.TTL)

	audiences := append([]string{req.Audience}, i.extraAudiences...)

	// Create JWT token with claims builder
	builder := jwt.NewBuilder().
		Subject(subject).
		IssuedAt(now).
		Audience(audiences).
		NotBefore(notBefore).
		Claim("use", "sig"). // required by Azure
		Expiration(notAfter)

	// Set issuer only if configured
	if i.issuer != nil {
		builder = builder.Issuer(*i.issuer)
	}

	// Build the token
	token, err := builder.Build()
	if err != nil {
		log.Errorf("Error creating JWT token: %v", err)
		return "", fmt.Errorf("error creating JWT token: %w", err)
	}

	signedToken, err := jwt.Sign(token,
		jwt.WithKey(i.signingKey.Algorithm(), i.signingKey))
	if err != nil {
		log.Errorf("Error signing JWT token: %v", err)
		return "", fmt.Errorf("error signing JWT token: %w", err)
	}

	return string(signedToken), nil
}

func signatureAlgorithmFrom(signer crypto.Signer) (jwa.SignatureAlgorithm, error) {
	switch k := signer.(type) {
	case *rsa.PrivateKey:
		bitSize := k.Size() * 8
		switch {
		case bitSize >= 4096:
			return jwa.RS512, nil
		case bitSize >= 3072:
			return jwa.RS384, nil
		default:
			return jwa.RS256, nil // consider deprecating support for RS256
		}
	case *ecdsa.PrivateKey:
		switch k.Curve.Params().BitSize {
		case 521:
			return jwa.ES512, nil
		case 384:
			return jwa.ES384, nil
		case 256:
			return jwa.ES256, nil
		default:
			return "", fmt.Errorf("unsupported ecdsa curve bit size: %d", k.Curve.Params().BitSize)
		}
	case ed25519.PrivateKey:
		return jwa.EdDSA, nil
	default:
		return "", fmt.Errorf("unsupported key type %T", signer)
	}
}
