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

package ca

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"

	"github.com/lestrrat-go/jwx/v2/jwa"
	"github.com/lestrrat-go/jwx/v2/jwk"
	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"k8s.io/client-go/kubernetes"

	"github.com/dapr/dapr/pkg/modes"
	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/dapr/pkg/security/spiffe"
	"github.com/dapr/dapr/pkg/sentry/config"
	"github.com/dapr/dapr/pkg/sentry/monitoring"
	bundle "github.com/dapr/dapr/pkg/sentry/server/ca/bundle"
	"github.com/dapr/dapr/pkg/sentry/server/ca/jwt"
	"github.com/dapr/dapr/utils"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.sentry.ca")

// SignRequest signs a certificate request with the issuer certificate.
type SignRequest struct {
	// Public key of the certificate request.
	PublicKey crypto.PublicKey

	// Signature of the certificate request.
	SignatureAlgorithm x509.SignatureAlgorithm

	// TrustDomain is the trust domain of the client.
	TrustDomain string

	// Namespace is the namespace of the client.
	Namespace string

	// AppID is the app id of the client.
	AppID string

	// Optional DNS names to add to the certificate.
	DNS []string
}

// Signer is the interface for the CA.
type Signer interface {
	// SignIdentity signs a certificate request with the issuer certificate. Note
	// that this does not include the trust anchors, and does not perform _any_
	// kind of validation on the request; authz should already have happened
	// before this point.
	// If given true, then the certificate duration will be given the largest
	// possible according to the signing certificate.
	SignIdentity(context.Context, *SignRequest) ([]*x509.Certificate, error)

	// TrustAnchors returns the trust anchors for the CA in PEM format.
	TrustAnchors() []byte

	// Extends signing to issue JWT tokens.
	jwt.Issuer
}

// store is the interface for the trust bundle backend store.
type store interface {
	store(context.Context, bundle.Bundle) error
	get(context.Context) (bundle.Bundle, error)
}

// ca is the implementation of the CA Signer.
type ca struct {
	bundle bundle.Bundle
	config config.Config
	jwt.Issuer
}

func New(ctx context.Context, conf config.Config) (Signer, error) {
	var castore store
	if conf.Mode == modes.KubernetesMode {
		log.Info("Using kubernetes secret store for trust bundle storage")

		client, err := kubernetes.NewForConfig(utils.GetConfig())
		if err != nil {
			return nil, err
		}

		castore = &kube{
			config:    conf,
			namespace: security.CurrentNamespace(),
			client:    client,
		}
	} else {
		log.Info("Using local file system for trust bundle storage")
		castore = &selfhosted{config: conf}
	}

	bndle, err := castore.get(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get CA bundle: %w", err)
	}

	var needsWrite bool
	if bndle.X509 == nil {
		needsWrite = true

		log.Info("Root and issuer certs not found: generating self signed CA")

		// Generate a key for X.509 certificates
		x509RootKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return nil, fmt.Errorf("failed to generate ECDSA key for X.509 certificates: %w", err)
		}

		certBundle, err := bundle.GenerateX509(bundle.OptionsX509{
			X509RootKey:      x509RootKey,
			TrustDomain:      conf.TrustDomain,
			AllowedClockSkew: conf.AllowedClockSkew,
			OverrideCATTL:    nil,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to generate CA bundle: %w", err)
		}

		bndle.X509 = certBundle

		log.Info("Generating self-signed CA certs and persisting to store")
	}

	if bndle.JWT == nil && conf.JWT.Enabled {
		needsWrite = true

		// Generate a key for JWT signing
		jwtRootKey, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			return nil, fmt.Errorf("failed to generate RSA key for JWT signing: %w", err)
		}
		conf.JWT.SigningAlgorithm = jwa.RS256.String()

		jwt, err := bundle.GenerateJWT(bundle.OptionsJWT{
			TrustDomain: conf.TrustDomain,
			JWTRootKey:  jwtRootKey,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to generate JWT bundle: %w", err)
		}

		bndle.JWT = jwt
		log.Info("Generating JWT signing key and persisting to store")
	}

	if needsWrite {
		if err := castore.store(ctx, bndle); err != nil {
			return nil, fmt.Errorf("failed to store CA bundle: %w", err)
		}
		log.Info("Self-signed certs and JWT root key generated and persisted successfully")

		monitoring.IssuerCertChanged()
	} else {
		log.Info("Root and issuer certs found: using credentials from store")
	}

	monitoring.IssuerCertExpiry(bndle.X509.IssChain[0].NotAfter)

	var jwtIss jwt.Issuer

	if conf.JWT.Enabled {
		log.Info("JWT issuing enabled")

		if bndle.JWT.SigningKey == nil {
			return nil, errors.New("JWT signing key not found in bundle")
		}
		if bndle.JWT.SigningKeyPEM == nil {
			return nil, errors.New("JWT signing key PEM not found in bundle")
		}
		if bndle.JWT.JWKS == nil {
			return nil, errors.New("JWKS not found in bundle")
		}
		if bndle.JWT.JWKSJson == nil {
			return nil, errors.New("JWKS JSON not found in bundle")
		}

		signKey, err := jwk.FromRaw(bndle.JWT.SigningKey)
		if err != nil {
			return nil, fmt.Errorf("failed to create JWK from key: %w", err)
		}

		// Verify that the signing key is compatible with the specified algorithm
		if conf.JWT.SigningAlgorithm == "" {
			return nil, errors.New("JWT signing algorithm required")
		}
		jwtSignAlg := jwa.SignatureAlgorithm(conf.JWT.SigningAlgorithm)
		if validateErr := validateKeyAlgorithmCompatibility(bndle.JWT.SigningKey, jwtSignAlg); validateErr != nil {
			return nil, fmt.Errorf("JWT signing key is incompatible with algorithm %s: %w", jwtSignAlg, validateErr)
		}

		// When a signing key is parsed from a pre-generated key, it often has no metadata
		// (e.g., key ID or algorithm). The user can set a key ID manually, but it must
		// uniquely match the intended key in the JWKS. If no key ID is provided, we
		// generate one from the key's thumbprint. In that case, the JWKS must also use
		// this generated key ID so the correct key can be matched.
		if signKey.KeyID() == "" {
			var kid string
			if conf.JWT.KeyID != nil {
				kid = *conf.JWT.KeyID
				log.Infof("Using JWT kid from configuration: %s", kid)
			} else {
				thumbprint, thumbErr := signKey.Thumbprint(bundle.DefaultKeyThumbprintAlgorithm)
				if thumbErr != nil {
					return nil, fmt.Errorf("failed to generate JWK thumbprint: %w", thumbErr)
				}
				kid = base64.StdEncoding.EncodeToString(thumbprint)
				log.Infof("Using JWT kid from thumbprint: %s, please ensure this aligns with your JWKS", kid)
			}

			if err = signKey.Set(jwk.KeyIDKey, kid); err != nil {
				return nil, fmt.Errorf("failed to set JWK key ID: %w", err)
			}
		}
		if err = signKey.Set(jwk.AlgorithmKey, conf.JWT.SigningAlgorithm); err != nil {
			return nil, fmt.Errorf("failed to set JWK algorithm: %w", err)
		}

		jwtIss, err = jwt.New(jwt.IssuerOptions{
			SignKey:          signKey,
			Issuer:           conf.JWT.Issuer,
			AllowedClockSkew: conf.AllowedClockSkew,
			JWKS:             bndle.JWT.JWKS,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create JWT issuer: %w", err)
		}
	}

	return &ca{
		bundle: bndle,
		config: conf,
		Issuer: jwtIss,
	}, nil
}

func (c *ca) SignIdentity(ctx context.Context, req *SignRequest) ([]*x509.Certificate, error) {
	td, err := spiffeid.TrustDomainFromString(req.TrustDomain)
	if err != nil {
		return nil, err
	}

	spiffeID, err := spiffe.FromStrings(td, req.Namespace, req.AppID)
	if err != nil {
		return nil, err
	}

	tmpl, err := bundle.GenerateWorkloadCert(req.SignatureAlgorithm, c.config.WorkloadCertTTL, c.config.AllowedClockSkew, spiffeID)
	if err != nil {
		return nil, err
	}
	tmpl.DNSNames = append(tmpl.DNSNames, req.DNS...)

	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, c.bundle.X509.IssChain[0], req.PublicKey, c.bundle.X509.IssKey)
	if err != nil {
		return nil, err
	}

	cert, err := x509.ParseCertificate(certDER)
	if err != nil {
		return nil, err
	}

	return append([]*x509.Certificate{cert}, c.bundle.X509.IssChain...), nil
}

// TODO: Remove this method in v1.12 since it is not used any more.
func (c *ca) TrustAnchors() []byte {
	return c.bundle.X509.TrustAnchors
}
