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
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/spiffe/go-spiffe/v2/spiffegrpc/grpccredentials"
	"github.com/spiffe/go-spiffe/v2/spiffeid"
)

// Parsed is a parsed SPIFFE ID according to the Dapr SPIFFE ID path format.
type Parsed struct {
	id spiffeid.ID

	namespace string
	appID     string
}

// FromGRPCContext parses a SPIFFE ID from a gRPC context.
func FromGRPCContext(ctx context.Context) (*Parsed, bool, error) {
	// Apply access control list filter
	id, ok := grpccredentials.PeerIDFromContext(ctx)
	if !ok {
		return nil, false, nil
	}

	split := strings.Split(id.Path(), "/")
	// Don't force match of 4 segments, since we may want to add more identifiers
	// to the path in future which would otherwise break backwards compat.
	if len(split) < 4 || split[0] != "" || split[1] != "ns" {
		return nil, false, fmt.Errorf("malformed SPIFFE ID: %s", id.String())
	}

	return &Parsed{
		id:        id,
		namespace: split[2],
		appID:     split[3],
	}, true, nil
}

// FromStrings builds a Dapr SPIFFE ID with the given namespace and app ID in
// the given Trust Domain.
func FromStrings(td spiffeid.TrustDomain, namespace, appID string) (*Parsed, error) {
	if len(td.String()) == 0 || len(namespace) == 0 || len(appID) == 0 {
		return nil, errors.New("malformed SPIFFE ID")
	}

	id, err := spiffeid.FromSegments(td, "ns", namespace, appID)
	if err != nil {
		return nil, err
	}

	return &Parsed{
		id:        id,
		namespace: namespace,
		appID:     appID,
	}, nil
}

func (p *Parsed) TrustDomain() spiffeid.TrustDomain {
	if p == nil {
		return spiffeid.TrustDomain{}
	}
	return p.id.TrustDomain()
}

func (p *Parsed) AppID() string {
	if p == nil {
		return ""
	}
	return p.appID
}

func (p *Parsed) Namespace() string {
	if p == nil {
		return ""
	}
	return p.namespace
}

func (p *Parsed) URL() *url.URL {
	if p == nil {
		return new(url.URL)
	}
	return p.id.URL()
}
