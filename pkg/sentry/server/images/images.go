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

// Package images defines the container images X.509 certificate extension
// which Sentry stamps into workload SVIDs. The extension value is a DER
// OCTET STRING whose contents are a JSON array of ContainerImage entries.
package images

import (
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/json"
	"errors"
	"fmt"
)

// OIDContainerImages is the Dapr container images certificate extension OID.
// It lives under the CNCF Private Enterprise Number arc (1.3.6.1.4.1.57683),
// in a Dapr project sub-arc (100) chosen to stay clear of the low-numbered
// sub-arcs Kubernetes uses for its certificate attributes.
// https://www.iana.org/assignments/enterprise-numbers/?q=57683
var OIDContainerImages = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 57683, 100, 1}

// Role is the role of a container in a Dapr workload pod.
type Role string

const (
	RoleDaprd Role = "daprd"
	RoleApp   Role = "app"
)

// ContainerImage records the image reference of a single container in the
// workload pod at certificate issuance time. Digest is best effort and may be
// empty if the container had not started when the certificate was issued.
type ContainerImage struct {
	Role          Role   `json:"role"`
	ContainerName string `json:"containerName"`
	Image         string `json:"image"`
	Digest        string `json:"digest,omitempty"`
}

// Extension encodes the given images as a non-critical certificate extension.
// Returns false if the list is empty, meaning no extension should be added.
func Extension(imgs []ContainerImage) (pkix.Extension, bool, error) {
	if len(imgs) == 0 {
		return pkix.Extension{}, false, nil
	}

	jsonBytes, err := json.Marshal(imgs)
	if err != nil {
		return pkix.Extension{}, false, fmt.Errorf("failed to marshal container images: %w", err)
	}

	value, err := asn1.Marshal(jsonBytes)
	if err != nil {
		return pkix.Extension{}, false, fmt.Errorf("failed to encode container images extension: %w", err)
	}

	return pkix.Extension{
		Id:       OIDContainerImages,
		Critical: false,
		Value:    value,
	}, true, nil
}

// FromExtensions decodes the container images extension from a certificate's
// parsed extensions. Returns false if the extension is not present.
func FromExtensions(exts []pkix.Extension) ([]ContainerImage, bool, error) {
	for _, ext := range exts {
		if !ext.Id.Equal(OIDContainerImages) {
			continue
		}

		var jsonBytes []byte
		rest, err := asn1.Unmarshal(ext.Value, &jsonBytes)
		if err != nil {
			return nil, false, fmt.Errorf("failed to decode container images extension: %w", err)
		}
		if len(rest) > 0 {
			return nil, false, errors.New("trailing data in container images extension")
		}

		var imgs []ContainerImage
		if err := json.Unmarshal(jsonBytes, &imgs); err != nil {
			return nil, false, fmt.Errorf("failed to unmarshal container images: %w", err)
		}

		return imgs, true, nil
	}

	return nil, false, nil
}
