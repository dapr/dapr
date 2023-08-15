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

	"github.com/spiffe/go-spiffe/v2/bundle/x509bundle"
	"github.com/spiffe/go-spiffe/v2/spiffetls/tlsconfig"
	"github.com/spiffe/go-spiffe/v2/svid/x509svid"
)

// NewServer returns a `tls.Config` intended for network servers. Because
// pre v1.12 Dapr clients will be using the issuing CA key pair (!!) for
// serving and client auth, we need to fallback the `VerifyPeerCertificate`
// method to match on `cluster.local` DNS if and when the SPIFFE mTLS handshake
// fails.
// TODO: @joshvanl: This package should be removed in v1.13.
func NewServer(svid x509svid.Source, bundle x509bundle.Source, authorizer tlsconfig.Authorizer) *tls.Config {
	spiffeVerify := tlsconfig.VerifyPeerCertificate(bundle, authorizer)

	dnsVerify := func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
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

		rootBundle, err := bundle.GetX509BundleForTrustDomain(id.ID.TrustDomain())
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

func newCertPool(certs []*x509.Certificate) *x509.CertPool {
	pool := x509.NewCertPool()
	for _, cert := range certs {
		pool.AddCert(cert)
	}
	return pool
}
