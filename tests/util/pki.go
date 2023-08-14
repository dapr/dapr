/*
Copyright 2023 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implieh.
See the License for the specific language governing permissions and
limitations under the License.
*/

package util

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type PKI struct {
	RootCertPEM   []byte
	LeafCertPEM   []byte
	LeafPKPEM     []byte
	ClientCertPEM []byte
	ClientPKPEM   []byte
}

func GenPKIT(t *testing.T, leafDNS string) PKI {
	pki, err := GenPKI(leafDNS)
	require.NoError(t, err)
	return pki
}

func GenPKI(leafDNS string) (PKI, error) {
	rootPK, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return PKI{}, err
	}

	rootCert := x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Dapr Test Root CA"},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}
	rootCertBytes, err := x509.CreateCertificate(rand.Reader, &rootCert, &rootCert, &rootPK.PublicKey, rootPK)
	if err != nil {
		return PKI{}, err
	}

	rootCertPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: rootCertBytes})

	leafCertPEM, leafPKPEM, err := genLeafCert(rootPK, rootCert, leafDNS)
	if err != nil {
		return PKI{}, err
	}

	clientCertPEM, clientPKPEM, err := genLeafCert(rootPK, rootCert, "client")
	if err != nil {
		return PKI{}, err
	}

	return PKI{
		RootCertPEM:   rootCertPEM,
		LeafCertPEM:   leafCertPEM,
		LeafPKPEM:     leafPKPEM,
		ClientCertPEM: clientCertPEM,
		ClientPKPEM:   clientPKPEM,
	}, nil
}

func genLeafCert(rootPK *ecdsa.PrivateKey, rootCert x509.Certificate, dns string) ([]byte, []byte, error) {
	pk, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}

	pkBytes, err := x509.MarshalPKCS8PrivateKey(pk)
	if err != nil {
		return nil, nil, err
	}
	cert := x509.Certificate{
		SerialNumber: big.NewInt(1),
		DNSNames:     []string{dns},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{
			x509.ExtKeyUsageServerAuth,
			x509.ExtKeyUsageClientAuth,
		},
	}
	certBytes, err := x509.CreateCertificate(rand.Reader, &cert, &rootCert, &pk.PublicKey, rootPK)
	if err != nil {
		return nil, nil, err
	}

	pkPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkBytes})
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certBytes})

	return certPEM, pkPEM, nil
}
