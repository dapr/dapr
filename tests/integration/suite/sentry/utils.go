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

package metadata

import (
	"crypto"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
)

func generateCSR(id string, privKey crypto.PrivateKey) ([]byte, error) {
	csr := x509.CertificateRequest{
		Subject:  pkix.Name{CommonName: id},
		DNSNames: []string{id},
	}
	csrDer, err := x509.CreateCertificateRequest(rand.Reader, &csr, privKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create sidecar csr: %w", err)
	}

	csrPem := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDer})
	return csrPem, nil
}
