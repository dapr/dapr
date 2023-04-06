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
	"errors"
	"fmt"
	"testing"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/stretchr/testify/assert"
)

func TestFromID(t *testing.T) {
	tests := map[string]struct {
		id     spiffeid.ID
		expOK  bool
		expErr error
		expID  Parsed
	}{
		"valid SPIFFE ID": {
			id:    spiffeid.RequireFromSegments(spiffeid.RequireTrustDomainFromString("example.org"), "ns", "test", "app"),
			expOK: true,
			expID: Parsed{
				TrustDomain: "example.org",
				Namespace:   "test",
				AppID:       "app",
			},
		},
		"valid SPIFFE ID with extra identifiers": {
			id:    spiffeid.RequireFromSegments(spiffeid.RequireTrustDomainFromString("example.org"), "ns", "test", "app", "extra", "identifiers"),
			expOK: true,
			expID: Parsed{
				TrustDomain: "example.org",
				Namespace:   "test",
				AppID:       "app",
			},
		},
		"SPIFFE ID: no namespace": {
			id:     spiffeid.RequireFromSegments(spiffeid.RequireTrustDomainFromString("example.org"), "test", "bar", "app"),
			expOK:  false,
			expErr: errors.New("malformed SPIFFE ID: spiffe://example.org/test/bar/app"),
			expID:  Parsed{},
		},
		"SPIFFE ID: too short": {
			id:     spiffeid.RequireFromPath(spiffeid.RequireTrustDomainFromString("example.org"), "/a"),
			expOK:  false,
			expErr: errors.New("malformed SPIFFE ID: spiffe://example.org/a"),
			expID:  Parsed{},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			id, ok, err := fromID(test.id)

			assert.Equal(t, test.expOK, ok)
			assert.Equal(t, test.expErr, err)
			assert.Equal(t, test.expID, id)
		})
	}
}

func TestToID(t *testing.T) {
	tests := map[string]struct {
		parsed Parsed
		expID  spiffeid.ID
		expErr error
	}{
		"valid parsed SPIFFE ID": {
			parsed: Parsed{
				TrustDomain: "example.org",
				Namespace:   "test",
				AppID:       "app",
			},
			expID: spiffeid.RequireFromSegments(spiffeid.RequireTrustDomainFromString("example.org"), "ns", "test", "app"),
		},
		"invalid trust domain": {
			parsed: Parsed{
				TrustDomain: "invalid^&%$^%$",
				Namespace:   "test",
				AppID:       "app",
			},
			expErr: fmt.Errorf("trust domain characters are limited to lowercase letters, numbers, dots, dashes, and underscores"),
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			id, err := test.parsed.ToID()

			assert.Equal(t, test.expID, id)
			assert.Equal(t, test.expErr, err)
		})
	}
}
