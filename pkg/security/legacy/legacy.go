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

package legacy

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"os"

	"github.com/spiffe/go-spiffe/v2/bundle/x509bundle"
	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/spiffe/go-spiffe/v2/spiffetls/tlsconfig"
	"github.com/spiffe/go-spiffe/v2/svid/x509svid"

	"github.com/dapr/dapr/pkg/security/consts"
)

// NewServer returns a `tls.Config` intended for network servers. Because
// pre v1.12 Dapr clients will be using the issuing CA key pair (!!) for
// serving and client auth, we need to fallback the `VerifyPeerCertificate`
// method to match on `cluster.local` DNS if and when the SPIFFE mTLS handshake
// fails.
// TODO: @joshvanl: This package should be removed in v1.13.
func NewServer(svid x509svid.Source, bundle x509bundle.Source, authorizer tlsconfig.Authorizer) *tls.Config {
	spiffeVerify := tlsconfig.VerifyPeerCertificate(bundle, authorizer)
	dnsVerify := dnsVerifyFn(svid, bundle)

	return &tls.Config{
		ClientAuth:     tls.RequireAnyClientCert,
		GetCertificate: tlsconfig.GetCertificate(svid),
		MinVersion:     tls.VersionTLS12,
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			// If SPIFFE verification fails, also attempt `cluster.local` DNS
			// verification.
			sErr := spiffeVerify(rawCerts, nil)
			if sErr != nil {
				dErr := dnsVerify(rawCerts, nil)
				if dErr != nil {
					return errors.Join(sErr, dErr)
				}
			}
			return nil
		},
	}
}

// NewDialClient returns a `tls.Config` intended for network clients. Because pre
// v1.12 Dapr servers will be using the issuing CA key pair (!!) for serving
// and client auth, we need to fallback the `VerifyPeerCertificate` method to
// match on `cluster.local` DNS if and when the SPIFFE mTLS handshake fails.
// TODO: @joshvanl: This package should be removed in v1.13.
func NewDialClient(svid x509svid.Source, bundle x509bundle.Source, authorizer tlsconfig.Authorizer) *tls.Config {
	tlsConfig := newDialClientNoClientAuth(svid, bundle, authorizer)
	tlsConfig.GetClientCertificate = tlsconfig.GetClientCertificate(svid)
	return tlsConfig
}

// NewDialClientOptionalClientAuth returns a `tls.Config` intended for network
// clients with optional client authentication. Because pre v1.12 Dapr servers
// will be using the issuing CA key pair (!!) for serving and client auth, we
// need to fallback the `VerifyPeerCertificate` method to match on
// `cluster.local` DNS if and when the SPIFFE mTLS handshake fails.
// Sets the client certificate to that configured in environment variables to satisfy
// sentry v1.11 servers.
func NewDialClientOptionalClientAuth(svid x509svid.Source, bundle x509bundle.Source, authorizer tlsconfig.Authorizer) (*tls.Config, error) {
	tlsConfig := newDialClientNoClientAuth(svid, bundle, authorizer)
	certPEM, cok := os.LookupEnv(consts.CertChainEnvVar)
	keyPEM, pok := os.LookupEnv(consts.CertKeyEnvVar)

	if cok && pok {
		cert, err := tls.X509KeyPair([]byte(certPEM), []byte(keyPEM))
		if err != nil {
			return nil, err
		}
		tlsConfig.GetClientCertificate = func(_ *tls.CertificateRequestInfo) (*tls.Certificate, error) {
			return &cert, nil
		}
	}

	return tlsConfig, nil
}

// newDialClientNoClientAuth returns a `tls.Config` intended for network clients
// without client authentication.
func newDialClientNoClientAuth(svid x509svid.Source, bundle x509bundle.Source, authorizer tlsconfig.Authorizer) *tls.Config {
	spiffeVerify := tlsconfig.VerifyPeerCertificate(bundle, authorizer)
	dnsVerify := dnsVerifyFn(svid, bundle)

	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		// Yep! We need to set this option because we are performing our own TLS
		// handshake verification, namely the SPIFFE ID validation, and then
		// falling back to the DNS verification `cluster.local`. See:
		// https://pkg.go.dev/crypto/tls#Config
		// This is not insecure (bar the poor DNS verification which is needed for
		// backwards compatibility).
		InsecureSkipVerify: true, //nolint:gosec
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			// If SPIFFE verification fails, also attempt `cluster.local` DNS
			// verification.
			sErr := spiffeVerify(rawCerts, nil)
			if sErr != nil {
				dErr := dnsVerify(rawCerts, nil)
				if dErr != nil {
					return errors.Join(sErr, dErr)
				}
			}
			return nil
		},
	}
}

func newCertPool(certs []*x509.Certificate) *x509.CertPool {
	pool := x509.NewCertPool()
	for _, cert := range certs {
		pool.AddCert(cert)
	}
	return pool
}

func dnsVerifyFn(svid x509svid.Source, bundle x509bundle.Source) func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
	return func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
		var certs []*x509.Certificate
		for _, rawCert := range rawCerts {
			cert, err := x509.ParseCertificate(rawCert)
			if err != nil {
				return err
			}
			certs = append(certs, cert)
		}
		if len(certs) == 0 {
			return errors.New("no certificates provided")
		}

		id, err := svid.GetX509SVID()
		if err != nil {
			return err
		}

		// Default to empty trust domain if no SPIFFE ID is present.
		td := spiffeid.TrustDomain{}
		if id != nil {
			td = id.ID.TrustDomain()
		}

		rootBundle, err := bundle.GetX509BundleForTrustDomain(td)
		if err != nil {
			return err
		}

		_, err = certs[0].Verify(x509.VerifyOptions{
			DNSName:       "cluster.local",
			Intermediates: newCertPool(certs[1:]),
			Roots:         newCertPool(rootBundle.X509Authorities()),
		})
		return err
	}
}
