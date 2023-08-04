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
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func genCrt(t *testing.T,
	name string,
	signCrt *x509.Certificate,
	signKey crypto.Signer,
) ([]byte, *x509.Certificate, []byte, crypto.Signer) {
	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	require.NoError(t, err)

	tmpl := x509.Certificate{
		Subject:               pkix.Name{Organization: []string{name}},
		SerialNumber:          serialNumber,
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(time.Hour),
	}

	pk, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	if signCrt == nil {
		signCrt = &tmpl
	}
	if signKey == nil {
		signKey = pk
	}

	crtDER, err := x509.CreateCertificate(rand.Reader, &tmpl, signCrt, pk.Public(), signKey)
	require.NoError(t, err)

	crt, err := x509.ParseCertificate(crtDER)
	require.NoError(t, err)
	crtPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: crtDER})

	pkDER, err := x509.MarshalPKCS8PrivateKey(pk)
	require.NoError(t, err)
	pkPEM := pem.EncodeToMemory(&pem.Block{
		Type: "PRIVATE KEY", Bytes: pkDER,
	})

	return crtPEM, crt, pkPEM, pk
}

func joinPEM(crts ...[]byte) []byte {
	var b []byte
	for _, crt := range crts {
		b = append(b, crt...)
	}
	return b
}

func TestVerifyBundle(t *testing.T) {
	rootPEM, rootCrt, _, rootPK := genCrt(t, "root", nil, nil)
	//nolint:dogsled
	rootBPEM, _, _, _ := genCrt(t, "rootB", nil, nil)
	int1PEM, int1Crt, int1PKPEM, int1PK := genCrt(t, "int1", rootCrt, rootPK)
	int2PEM, int2Crt, int2PKPEM, int2PK := genCrt(t, "int2", int1Crt, int1PK)

	tests := map[string]struct {
		issChainPEM []byte
		issKeyPEM   []byte
		trustBundle []byte
		expErr      bool
		expBundle   Bundle
	}{
		"if issuer chain pem empty, expect error": {
			issChainPEM: nil,
			issKeyPEM:   int1PKPEM,
			trustBundle: rootPEM,
			expErr:      true,
			expBundle:   Bundle{},
		},
		"if issuer key pem empty, expect error": {
			issChainPEM: int1PEM,
			issKeyPEM:   nil,
			trustBundle: rootPEM,
			expErr:      true,
			expBundle:   Bundle{},
		},
		"if issuer trust bundle pem empty, expect error": {
			issChainPEM: int1PEM,
			issKeyPEM:   int1PKPEM,
			trustBundle: nil,
			expErr:      true,
			expBundle:   Bundle{},
		},
		"invalid issuer chain PEM should error": {
			issChainPEM: []byte("invalid"),
			issKeyPEM:   int1PKPEM,
			trustBundle: rootPEM,
			expErr:      true,
			expBundle:   Bundle{},
		},
		"invalid issuer key PEM should error": {
			issChainPEM: int1PEM,
			issKeyPEM:   []byte("invalid"),
			trustBundle: rootPEM,
			expErr:      true,
			expBundle:   Bundle{},
		},
		"invalid trust bundle PEM should error": {
			issChainPEM: int1PEM,
			issKeyPEM:   int1PKPEM,
			trustBundle: []byte("invalid"),
			expErr:      true,
			expBundle:   Bundle{},
		},
		"if issuer chain is in wrong order, expect error": {
			issChainPEM: joinPEM(int1PEM, int2PEM),
			issKeyPEM:   int2PKPEM,
			trustBundle: joinPEM(rootPEM, rootBPEM),
			expErr:      true,
			expBundle:   Bundle{},
		},
		"if issuer key does not belong to issuer certificate, expect error": {
			issChainPEM: joinPEM(int2PEM, int1PEM),
			issKeyPEM:   int1PKPEM,
			trustBundle: joinPEM(rootPEM, rootBPEM),
			expErr:      true,
			expBundle:   Bundle{},
		},
		"if trust anchors contains non root certificates, exp error": {
			issChainPEM: joinPEM(int2PEM, int1PEM),
			issKeyPEM:   int2PKPEM,
			trustBundle: joinPEM(rootPEM, rootBPEM, int1PEM),
			expErr:      true,
			expBundle:   Bundle{},
		},
		"if issuer chain doesn't belong to trust anchors, expect error": {
			issChainPEM: joinPEM(int2PEM, int1PEM),
			issKeyPEM:   int2PKPEM,
			trustBundle: joinPEM(rootBPEM),
			expErr:      true,
			expBundle:   Bundle{},
		},
		"valid chain should not error": {
			issChainPEM: int1PEM,
			issKeyPEM:   int1PKPEM,
			trustBundle: rootPEM,
			expErr:      false,
			expBundle: Bundle{
				TrustAnchors: rootPEM,
				IssChainPEM:  joinPEM(int1PEM),
				IssKeyPEM:    int1PKPEM,
				IssChain:     []*x509.Certificate{int1Crt},
				IssKey:       int1PK,
			},
		},
		"valid long chain should not error": {
			issChainPEM: joinPEM(int2PEM, int1PEM),
			issKeyPEM:   int2PKPEM,
			trustBundle: joinPEM(rootPEM, rootBPEM),
			expErr:      false,
			expBundle: Bundle{
				TrustAnchors: joinPEM(rootPEM, rootBPEM),
				IssChainPEM:  joinPEM(int2PEM, int1PEM),
				IssKeyPEM:    int2PKPEM,
				IssChain:     []*x509.Certificate{int2Crt, int1Crt},
				IssKey:       int2PK,
			},
		},
		"is root certificate in chain, expect to be removed": {
			issChainPEM: joinPEM(int2PEM, int1PEM, rootPEM),
			issKeyPEM:   int2PKPEM,
			trustBundle: joinPEM(rootPEM, rootBPEM),
			expErr:      false,
			expBundle: Bundle{
				TrustAnchors: joinPEM(rootPEM, rootBPEM),
				IssChainPEM:  joinPEM(int2PEM, int1PEM),
				IssKeyPEM:    int2PKPEM,
				IssChain:     []*x509.Certificate{int2Crt, int1Crt},
				IssKey:       int2PK,
			},
		},
		"comments are removed from parsed issuer chain, private key and trust anchors": {
			issChainPEM: joinPEM(
				[]byte("# this is a comment\n"),
				int2PEM,
				[]byte("# this is a comment\n"),
				int1PEM,
				[]byte("# this is a comment\n"),
			),
			issKeyPEM: joinPEM(
				[]byte("# this is a comment\n"),
				int2PKPEM,
				[]byte("# this is a comment\n"),
			),
			trustBundle: joinPEM(
				[]byte("# this is a comment\n"),
				rootPEM,
				[]byte("# this is a comment\n"),
				rootBPEM,
				[]byte("# this is a comment\n"),
			),
			expErr: false,
			expBundle: Bundle{
				TrustAnchors: joinPEM(rootPEM, rootBPEM),
				IssChainPEM:  joinPEM(int2PEM, int1PEM),
				IssKeyPEM:    int2PKPEM,
				IssChain:     []*x509.Certificate{int2Crt, int1Crt},
				IssKey:       int2PK,
			},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			Bundle, err := verifyBundle(test.trustBundle, test.issChainPEM, test.issKeyPEM)
			assert.Equal(t, test.expErr, err != nil, "%v", err)
			require.Equal(t, test.expBundle, Bundle)
		})
	}
}
