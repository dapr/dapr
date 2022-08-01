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

// This utility adapted from https://go.dev/src/crypto/tls/generate_cert.go

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
	"net"
	"os"
	"path"
	"strings"
	"time"
)

const (
	// clockSkew is the margin of error for checking whether a certificate is valid.
	clockSkew = time.Minute * 5
)

// GenerateTLSCertAndKey generates a self-signed X.509 certificate for a TLS server.
// Outputs to 'cert.pem' and 'key.pem' and will overwrite existing files.
//
// host: Comma-separated hostnames and IPs to generate a certificate for
// validFrom: The time the certificate is valid from
// validFor: The duration the certificate is valid for
// directory: Path to write the files to
func GenerateTLSCertAndKey(host string, validFrom time.Time, validFor time.Duration, directory string) error {
	// *********************
	// Generate private key
	// *********************
	tlsKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}

	b, err := x509.MarshalPKCS8PrivateKey(tlsKey)
	if err != nil {
		log.Printf("Unable to marshal ECDSA private key: %v", err)
		return err
	}

	if err = bytesToPemFile(path.Join(directory, "key.pem"), "PRIVATE KEY", b); err != nil {
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
		},
		NotBefore:             validFrom.Add(-clockSkew),
		NotAfter:              validFrom.Add(validFor).Add(clockSkew),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}

	hosts := strings.Split(host, ",")
	for _, h := range hosts {
		if ip := net.ParseIP(h); ip != nil {
			certTemplate.IPAddresses = append(certTemplate.IPAddresses, ip)
		} else {
			certTemplate.DNSNames = append(certTemplate.DNSNames, h)
		}
	}

	certBytes, err := x509.CreateCertificate(rand.Reader, &certTemplate, &certTemplate, &tlsKey.PublicKey, tlsKey)
	if err != nil {
		log.Printf("Unable to create certificate: %v", err)
		return err
	}

	if err := bytesToPemFile(path.Join(directory, "cert.pem"), "CERTIFICATE", certBytes); err != nil {
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
