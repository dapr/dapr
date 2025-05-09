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
	"crypto/x509"
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
	JWTIssuer
}

type JWTIssuer interface {
	// GenerateJWT creates a JWT token for the given request. The token includes
	// claims based on the identity information provided in the request.
	GenerateJWT(context.Context, *JWTRequest) (string, error)

	// JWKS returns the JSON Web Key Set (JWKS).
	JWKS() []byte

	// JWTSignatureAlgorithm returns the signature algorithm used for signing JWTs.
	JWTSignatureAlgorithm() jwa.KeyAlgorithm
}

// generate represents the type of credentials that require generation.
type generate struct {
	x509 bool
	jwt  bool
}

func (g *generate) Any() bool {
	return g.x509 || g.jwt
}

func (g *generate) X509() bool {
	return g.x509
}

func (g *generate) JWT() bool {
	return g.jwt
}

// store is the interface for the trust bundle backend store.
type store interface {
	store(context.Context, Bundle) error
	get(context.Context) (Bundle, generate, error)
}

// ca is the implementation of the CA Signer.
type ca struct {
	bundle Bundle
	config config.Config
	jwtIssuer
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

	bundle, gen, err := castore.get(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get CA bundle: %w", err)
	}

	if gen.Any() {
		log.Info("Root and issuer certs not found: generating self signed CA")

		rootKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return nil, err
		}

		genBundle, err := GenerateBundle(rootKey, conf.TrustDomain, conf.AllowedClockSkew, nil, gen)
		if err != nil {
			return nil, fmt.Errorf("failed to generate CA bundle: %w", err)
		}
		bundle.Merge(genBundle)

		log.Info("Merged provided and generated CA bundle")

		if err := castore.store(ctx, bundle); err != nil {
			return nil, fmt.Errorf("failed to store CA bundle: %w", err)
		}

		log.Info("Self-signed certs generated and persisted successfully")
		monitoring.IssuerCertChanged()
	} else {
		log.Info("Root and issuer certs found: using credentials from store")
	}
	monitoring.IssuerCertExpiry(bundle.IssChain[0].NotAfter)

	var jwtIssuer jwtIssuer
	if conf.JWTEnabled {
		log.Info("JWT signing enabled")

		if bundle.JWTSigningKey == nil {
			return nil, fmt.Errorf("JWT signing key not found in bundle")
		}
		if bundle.JWTSigningKeyPEM == nil {
			return nil, fmt.Errorf("JWT signing key PEM not found in bundle")
		}
		if bundle.JWKS == nil {
			return nil, fmt.Errorf("JWKS not found in bundle")
		}
		if bundle.JWKSJson == nil {
			return nil, fmt.Errorf("JWKS JSON not found in bundle")
		}

		signKey, err := jwk.FromRaw(bundle.JWTSigningKey)
		if err != nil {
			return nil, fmt.Errorf("failed to create JWK from key: %w", err)
		}
		signKey.Set(jwk.KeyIDKey, conf.JWTKeyID)
		signKey.Set(jwk.AlgorithmKey, conf.JWTSigningAlgorithm)

		jwtIssuer, err = NewJWTIssuer(
			signKey,
			conf.JWTIssuer,
			conf.AllowedClockSkew,
			conf.JWTAudiences)
		if err != nil {
			return nil, fmt.Errorf("failed to create JWT issuer: %w", err)
		}
	}

	return &ca{
		bundle:    bundle,
		config:    conf,
		jwtIssuer: jwtIssuer,
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

	tmpl, err := generateWorkloadCert(req.SignatureAlgorithm, c.config.WorkloadCertTTL, c.config.AllowedClockSkew, spiffeID)
	if err != nil {
		return nil, err
	}
	tmpl.DNSNames = append(tmpl.DNSNames, req.DNS...)

	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, c.bundle.IssChain[0], req.PublicKey, c.bundle.IssKey)
	if err != nil {
		return nil, err
	}

	cert, err := x509.ParseCertificate(certDER)
	if err != nil {
		return nil, err
	}

	return append([]*x509.Certificate{cert}, c.bundle.IssChain...), nil
}

// TODO: Remove this method in v1.12 since it is not used any more.
func (c *ca) TrustAnchors() []byte {
	return c.bundle.TrustAnchors
}

func (c *ca) JWKS() []byte {
	return c.bundle.JWKSJson
}

func (c *ca) JWTSignatureAlgorithm() jwa.KeyAlgorithm {
	if c.jwtIssuer.signKey == nil {
		return nil
	}
	return c.jwtIssuer.signKey.Algorithm()
}
