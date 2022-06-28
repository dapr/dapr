/*
Copyright 2022 The Dapr Authors
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

package utils

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"log"
	"math/big"
	"os"
	"time"
)

// GenerateTLSCertAndKey creates two new files
// 1. cert.pem: contains the TLS certificate
// 2. key.pem: contains the TLS key
// If the files already exist, they are overwritten.
func GenerateTLSCertAndKey(validFrom time.Time, validFor time.Duration) error {
	// *********************
	// Generate private key
	// *********************
	tlsKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}

	b, err := x509.MarshalECPrivateKey(tlsKey)
	if err != nil {
		log.Printf("Unable to marshal ECDSA private key: %v", err)
		return err
	}

	if err := bytesToPemFile("key.pem", "EC PRIVATE KEY", b); err != nil {
		log.Printf("Unable to write key.pem: %v", err)
		return err
	}

	// *********************
	// Generate certificate
	// *********************
	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	if err != nil {
		log.Fatalf("failed to generate serial number: %s", err)
	}

	certTemplate := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{"Dapr"},
			CommonName:   "Root CA",
		},
		NotBefore:             validFrom,
		NotAfter:              validFrom.Add(validFor),
		KeyUsage:              x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}

	certBytes, err := x509.CreateCertificate(rand.Reader, &certTemplate, &certTemplate, &tlsKey.PublicKey, tlsKey)
	if err != nil {
		log.Printf("Unable to create certificate: %v", err)
		return err
	}

	if err := bytesToPemFile("cert.pem", "CERTIFICATE", certBytes); err != nil {
		log.Printf("Unable to write cert.pem: %v", err)
		return err
	}

	return nil
}

func bytesToPemFile(filename string, pemBlockType string, bytes []byte) error {
	f, err := os.Create(filename)
	if err != nil {
		return err
	}
	if err := pem.Encode(f, &pem.Block{Type: pemBlockType, Bytes: bytes}); err != nil {
		return err
	}
	return f.Close()
}
