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

package security

import (
	"context"
	"fmt"

	spiffecontext "github.com/dapr/kit/crypto/spiffe/context"
	"github.com/spiffe/go-spiffe/v2/svid/jwtsvid"
)

// JWTFetcher fetches a JWT for the given audience.
// The returned string is the raw JWT compact serialisation (header.payload.signature).
type JWTFetcher interface {
	FetchJWT(ctx context.Context, audience string) (string, error)
}

// SPIFFEJWTFetcher fetches SPIFFE JWT SVIDs using the security Handler.
// When the handler is nil (security disabled), FetchJWT returns an error.
type SPIFFEJWTFetcher struct {
	sec Handler
}

// NewSPIFFEJWTFetcher returns a fetcher backed by the given security handler.
func NewSPIFFEJWTFetcher(sec Handler) *SPIFFEJWTFetcher {
	return &SPIFFEJWTFetcher{sec: sec}
}

// FetchJWT fetches a SPIFFE JWT SVID for the given audience.
// The returned string is the raw JWT compact serialisation (header.payload.signature).
func (f *SPIFFEJWTFetcher) FetchJWT(ctx context.Context, audience string) (string, error) {
	if f.sec == nil {
		return "", fmt.Errorf("SPIFFE JWT auth is configured but no security handler is available")
	}
	ctxWithSVID := f.sec.WithSVIDContext(ctx)
	jwtSource, ok := spiffecontext.JWTFrom(ctxWithSVID)
	if !ok {
		return "", fmt.Errorf("SPIFFE JWT source not available in context")
	}
	svid, err := jwtSource.FetchJWTSVID(ctxWithSVID, jwtsvid.Params{Audience: audience})
	if err != nil {
		return "", fmt.Errorf("failed to fetch SPIFFE JWT SVID: %w", err)
	}
	return svid.Marshal(), nil
}
