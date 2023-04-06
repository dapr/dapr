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
	"encoding/pem"
	"fmt"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/dapr/pkg/security/spiffe"
	"github.com/dapr/dapr/pkg/sentry/config"
	"github.com/dapr/dapr/pkg/sentry/monitoring"
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

// Interface is the interface for the CA.
type Interface interface {
	// SignIdentity signs a certificate request with the issuer certificate. Note
	// that this does not include the trust anchors, and does not perform _any_
	// kind of validation on the request; authz should already have happened
	// before this point.
	SignIdentity(context.Context, *SignRequest) ([]*x509.Certificate, error)

	// TrustAnchors returns the trust anchors for the CA in PEM format.
	TrustAnchors() []byte
}

// store is the interface for the trust bundle backend store.
type store interface {
	store(context.Context, caBundle) error
	get(context.Context) (caBundle, bool, error)
}

// ca is the implementation of the CA Interface.
type ca struct {
	bundle caBundle
	config config.Config
}

// caBundle is the bundle of certificates and keys used by the CA.
type caBundle struct {
	trustAnchors []byte
	issChainPEM  []byte
	issKeyPEM    []byte
	issChain     []*x509.Certificate
	issKey       any
}

func New(ctx context.Context, conf config.Config) (Interface, error) {
	var castore store
	if config.IsKubernetesHosted() {
		log.Info("Using kubernetes secret store for trust bundle storage")

		restConfig, err := rest.InClusterConfig()
		if err != nil {
			return nil, err
		}
		client, err := kubernetes.NewForConfig(restConfig)
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

		bundle, err = generateCABundle(conf)
		if err != nil {
			return nil, err
		}

		log.Info("Root and issuer certs generated")

		if err := castore.store(ctx, bundle); err != nil {
			return nil, err
		}

		log.Info("Self-signed certs generated and persisted successfully")
		monitoring.IssuerCertChanged()
		monitoring.IssuerCertExpiry(bundle.issChain[0].NotAfter)
	} else {
		log.Info("Root and issuer certs found: using existing certs")
	}

	return &ca{
		bundle: bundle,
		config: conf,
	}, nil
}

func (c *ca) SignIdentity(ctx context.Context, req *SignRequest) ([]*x509.Certificate, error) {
	spiffeID, err := (spiffe.Parsed{
		TrustDomain: req.TrustDomain,
		Namespace:   req.Namespace,
		AppID:       req.AppID,
	}).ToID()
	if err != nil {
		return nil, err
	}

	tmpl, err := generateWorkloadCert(req.SignatureAlgorithm, c.config.WorkloadCertTTL, c.config.AllowedClockSkew, spiffeID)
	if err != nil {
		return nil, err
	}
	tmpl.DNSNames = append(tmpl.DNSNames, req.DNS...)

	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, c.bundle.issChain[0], req.PublicKey, c.bundle.issKey)
	if err != nil {
		return nil, err
	}

	cert, err := x509.ParseCertificate(certDER)
	if err != nil {
		return nil, err
	}

	return append([]*x509.Certificate{cert}, c.bundle.issChain...), nil
}

// TODO: Remove this method in v1.12 since it is not used any more.
func (c *ca) TrustAnchors() []byte {
	return c.bundle.trustAnchors
}

func generateCABundle(conf config.Config) (caBundle, error) {
	rootKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return caBundle{}, err
	}

	rootCert, err := GenerateRootCert(conf.TrustDomain, conf.AllowedClockSkew)
	if err != nil {
		return caBundle{}, fmt.Errorf("failed to generate root cert: %w", err)
	}

	rootCertDER, err := x509.CreateCertificate(rand.Reader, rootCert, rootCert, &rootKey.PublicKey, rootKey)
	if err != nil {
		return caBundle{}, fmt.Errorf("failed to sign root certificate: %w", err)
	}
	trustAnchors := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: rootCertDER})

	issKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return caBundle{}, err
	}
	issKeyDer, err := x509.MarshalPKCS8PrivateKey(issKey)
	if err != nil {
		return caBundle{}, err
	}
	issKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: issKeyDer})

	issCert, err := GenerateIssuerCert(conf.TrustDomain, conf.AllowedClockSkew)
	if err != nil {
		return caBundle{}, fmt.Errorf("failed to generate issuer cert: %w", err)
	}
	issCertDER, err := x509.CreateCertificate(rand.Reader, issCert, rootCert, &issKey.PublicKey, rootKey)
	if err != nil {
		return caBundle{}, fmt.Errorf("failed to sign issuer cert: %w", err)
	}
	issCertPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: issCertDER})

	issCert, err = x509.ParseCertificate(issCertDER)
	if err != nil {
		return caBundle{}, err
	}

	return caBundle{
		trustAnchors: trustAnchors,
		issChainPEM:  issCertPEM,
		issKeyPEM:    issKeyPEM,
		issChain:     []*x509.Certificate{issCert},
		issKey:       issKey,
	}, nil
}
