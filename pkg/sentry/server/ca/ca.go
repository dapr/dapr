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
	"time"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"k8s.io/client-go/kubernetes"

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
	// TODO: @joshvanl: Remove bool value in v1.13 as the inject no longer needs
	// to request other identities.
	SignIdentity(context.Context, *SignRequest, bool) ([]*x509.Certificate, error)

	// TrustAnchors returns the trust anchors for the CA in PEM format.
	TrustAnchors() []byte
}

// store is the interface for the trust bundle backend store.
type store interface {
	store(context.Context, Bundle) error
	get(context.Context) (Bundle, bool, error)
}

// ca is the implementation of the CA Signer.
type ca struct {
	bundle Bundle
	config config.Config
}

func New(ctx context.Context, conf config.Config) (Signer, error) {
	var castore store
	if config.IsKubernetesHosted() {
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

	bundle, ok, err := castore.get(ctx)
	if err != nil {
		return nil, err
	}

	if !ok {
		log.Info("Root and issuer certs not found: generating self signed CA")

		rootKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return nil, err
		}

		bundle, err = GenerateBundle(rootKey, conf.TrustDomain, conf.AllowedClockSkew, nil)
		if err != nil {
			return nil, err
		}

		log.Info("Root and issuer certs generated")

		if err := castore.store(ctx, bundle); err != nil {
			return nil, err
		}

		log.Info("Self-signed certs generated and persisted successfully")
		monitoring.IssuerCertChanged()
	} else {
		log.Info("Root and issuer certs found: using existing certs")
	}
	monitoring.IssuerCertExpiry(bundle.IssChain[0].NotAfter)

	return &ca{
		bundle: bundle,
		config: conf,
	}, nil
}

func (c *ca) SignIdentity(ctx context.Context, req *SignRequest, overrideDuration bool) ([]*x509.Certificate, error) {
	td, err := spiffeid.TrustDomainFromString(req.TrustDomain)
	if err != nil {
		return nil, err
	}

	spiffeID, err := spiffe.FromStrings(td, req.Namespace, req.AppID)
	if err != nil {
		return nil, err
	}

	dur := c.config.WorkloadCertTTL
	if overrideDuration {
		dur = time.Until(c.bundle.IssChain[0].NotAfter)
	}

	tmpl, err := generateWorkloadCert(req.SignatureAlgorithm, dur, c.config.AllowedClockSkew, spiffeID)
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
