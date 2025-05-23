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
	ca_bundle "github.com/dapr/dapr/pkg/sentry/server/ca/bundle"
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
	store(context.Context, ca_bundle.Bundle) error
	get(context.Context) (ca_bundle.Bundle, ca_bundle.MissingCredentials, error)
}

// ca is the implementation of the CA Signer.
type ca struct {
	bundle ca_bundle.Bundle
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

	bundle, missingCreds, err := castore.get(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get CA bundle: %w", err)
	}

	if missingCreds.MissingRootKeys() {
		log.Info("Root and issuer certs not found: generating self signed CA")

		// Generate a key for X.509 certificates
		x509RootKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return nil, fmt.Errorf("failed to generate ECDSA key for X.509 certificates: %w", err)
		}

		// Generate a key for JWT signing
		jwtRootKey, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			return nil, fmt.Errorf("failed to generate RSA key for JWT signing: %w", err)
		}
		conf.JWT.SigningAlgorithm = jwa.RS256.String()

		generatedBundle, err := ca_bundle.Generate(ca_bundle.GenerateOptions{
			X509RootKey:        x509RootKey,
			JWTRootKey:         jwtRootKey,
			TrustDomain:        conf.TrustDomain,
			AllowedClockSkew:   conf.AllowedClockSkew,
			OverrideCATTL:      nil,
			MissingCredentials: missingCreds,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to generate CA bundle: %w", err)
		}

		if missingCreds.X509 {
			bundle.X509 = generatedBundle.X509
		}
		if missingCreds.JWT {
			bundle.JWT = generatedBundle.JWT
		}

		if err := castore.store(ctx, bundle); err != nil {
			return nil, fmt.Errorf("failed to store CA bundle: %w", err)
		}

		log.Info("Self-signed certs generated and persisted successfully")
		monitoring.IssuerCertChanged()
	} else {
		log.Info("Root and issuer certs found: using credentials from store")
	}
	monitoring.IssuerCertExpiry(bundle.X509.IssChain[0].NotAfter)

	var jwtIss jwt.Issuer
	if conf.JWT.Enabled {
		log.Info("JWT issuing enabled")

		if bundle.JWT.SigningKey == nil {
			return nil, errors.New("JWT signing key not found in bundle")
		}
		if bundle.JWT.SigningKeyPEM == nil {
			return nil, errors.New("JWT signing key PEM not found in bundle")
		}
		if bundle.JWT.JWKS == nil {
			return nil, errors.New("JWKS not found in bundle")
		}
		if bundle.JWT.JWKSJson == nil {
			return nil, errors.New("JWKS JSON not found in bundle")
		}

		signKey, err := jwk.FromRaw(bundle.JWT.SigningKey)
		if err != nil {
			return nil, fmt.Errorf("failed to create JWK from key: %w", err)
		}

		// Verify that the signing key is compatible with the specified algorithm
		if conf.JWT.SigningAlgorithm == "" {
			return nil, errors.New("JWT signing algorithm required")
		}
		jwtSignAlg := jwa.SignatureAlgorithm(conf.JWT.SigningAlgorithm)
		if validateErr := validateKeyAlgorithmCompatibility(bundle.JWT.SigningKey, jwtSignAlg); validateErr != nil {
			return nil, fmt.Errorf("JWT signing key is incompatible with algorithm %s: %w", jwtSignAlg, validateErr)
		}

		var kid string
		if conf.JWT.KeyID != nil {
			kid = *conf.JWT.KeyID
		} else {
			thumbprint, thumbErr := signKey.Thumbprint(ca_bundle.DefaultKeyThumbprintAlgorithm)
			if thumbErr != nil {
				return nil, fmt.Errorf("failed to generate JWK thumbprint: %w", thumbErr)
			}
			kid = base64.StdEncoding.EncodeToString(thumbprint)
		}

		signKey.Set(jwk.KeyIDKey, kid)
		signKey.Set(jwk.AlgorithmKey, conf.JWT.SigningAlgorithm)

		jwtIss, err = jwt.New(jwt.IssuerOptions{
			SignKey:          signKey,
			Issuer:           conf.JWT.Issuer,
			AllowedClockSkew: conf.AllowedClockSkew,
			JWKS:             bundle.JWT.JWKS,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create JWT issuer: %w", err)
		}
	}

	return &ca{
		bundle: bundle,
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

	tmpl, err := ca_bundle.GenerateWorkloadCert(req.SignatureAlgorithm, c.config.WorkloadCertTTL, c.config.AllowedClockSkew, spiffeID)
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
