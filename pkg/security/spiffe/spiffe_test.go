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
	"testing"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/stretchr/testify/assert"
)

func TestFromStrings(t *testing.T) {
	tests := map[string]struct {
		td     spiffeid.TrustDomain
		appID  string
		ns     string
		expErr error
		expID  *Parsed
	}{
		"valid SPIFFE ID": {
			td:    spiffeid.RequireTrustDomainFromString("example.org"),
			ns:    "test",
			appID: "app",
			expID: &Parsed{
				id:        spiffeid.RequireFromString("spiffe://example.org/ns/test/app"),
				namespace: "test",
				appID:     "app",
			},
		},
		"SPIFFE ID: no namespace": {
			td:     spiffeid.RequireTrustDomainFromString("example.org"),
			ns:     "",
			appID:  "app",
			expErr: errors.New("malformed SPIFFE ID"),
			expID:  nil,
		},
		"SPIFFE ID: no app ID": {
			td:     spiffeid.RequireTrustDomainFromString("example.org"),
			ns:     "test",
			appID:  "",
			expErr: errors.New("malformed SPIFFE ID"),
			expID:  nil,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			id, err := FromStrings(test.td, test.ns, test.appID)
			assert.Equal(t, test.expErr, err)
			assert.Equal(t, test.expID, id)
		})
	}
}
