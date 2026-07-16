/*
Copyright 2026 The Dapr Authors
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

package signing

import (
	"crypto/x509"
	"encoding/asn1"
	"errors"
	"fmt"
	"time"
)

type certValidityWindow struct {
	notBefore time.Time
	notAfter  time.Time
}

func (s *Signing) certChainTrustVerified(digest []byte, eventTS time.Time) bool {
	v, ok := s.certVerifyCache.Load(string(digest))
	if !ok {
		return false
	}
	w := v.(certValidityWindow)
	return !eventTS.Before(w.notBefore) && !eventTS.After(w.notAfter)
}

func (s *Signing) cacheCertChainTrust(digest []byte, chainDER []byte) {
	leaf, err := parseLeafCertFromChainDER(chainDER)
	if err != nil {
		// Parsing failure here is benign - we just don't cache. The
		// next attestation using the same cert will pay full chain-of-
		// trust verification again. Log so the failure isn't entirely
		// silent.
		log.Warnf("Workflow actor '%s': failed to parse leaf cert for chain-of-trust cache, will re-verify on next attestation: %s", s.ActorID, err)
		return
	}
	s.certVerifyCache.Store(string(digest), certValidityWindow{
		notBefore: leaf.NotBefore,
		notAfter:  leaf.NotAfter,
	})
}

// parseLeafCertFromChainDER parses only the leaf (first) cert from a
// DER-concatenated chain, matching the format used by
// SigningCertificate.certificate and the attestation companion. Each cert
// is a self-delimiting ASN.1 SEQUENCE, so we read only the first.
func parseLeafCertFromChainDER(chainDER []byte) (*x509.Certificate, error) {
	if len(chainDER) == 0 {
		return nil, errors.New("certificate chain is empty")
	}
	var raw asn1.RawValue
	if _, err := asn1.Unmarshal(chainDER, &raw); err != nil {
		return nil, fmt.Errorf("failed to read leaf certificate ASN.1: %w", err)
	}
	return x509.ParseCertificate(raw.FullBytes)
}
