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

package spiffe

import (
	"context"
	"fmt"
	"strings"

	"github.com/spiffe/go-spiffe/v2/spiffegrpc/grpccredentials"
	"github.com/spiffe/go-spiffe/v2/spiffeid"
)

// Parsed is a parsed SPIFFE ID according to the Dapr SPIFFE ID path format.
type Parsed struct {
	TrustDomain string
	Namespace   string
	AppID       string
}

// FromContext parses a SPIFFE ID from a gRPC context.
func FromContext(ctx context.Context) (Parsed, bool, error) {
	// Apply access control list filter
	id, ok := grpccredentials.PeerIDFromContext(ctx)
	if !ok {
		return Parsed{}, false, nil
	}

	return fromID(id)
}

func fromID(id spiffeid.ID) (Parsed, bool, error) {
	split := strings.Split(id.Path(), "/")
	// Don't force match of 4 segments, since we may want to add more identifiers
	// to the path in future which would otherwise break backwards compat.
	if len(split) < 4 || split[0] != "" || split[1] != "ns" {
		return Parsed{}, false, fmt.Errorf("malformed SPIFFE ID: %s", id.String())
	}

	return Parsed{
		TrustDomain: id.TrustDomain().String(),
		Namespace:   split[2],
		AppID:       split[3],
	}, true, nil
}

func (p Parsed) ToID() (spiffeid.ID, error) {
	td, err := spiffeid.TrustDomainFromString(p.TrustDomain)
	if err != nil {
		return spiffeid.ID{}, err
	}

	return spiffeid.FromSegments(td, "ns", p.Namespace, p.AppID)
}
