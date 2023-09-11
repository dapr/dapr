//go:build unit
// +build unit

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

package fake

import (
	"context"
	"crypto/x509"

	"github.com/dapr/dapr/pkg/sentry/server/ca"
)

type Fake struct {
	signIdentityFn func(context.Context, *ca.SignRequest, bool) ([]*x509.Certificate, error)
	trustAnchorsFn func() []byte
}

func New() *Fake {
	return &Fake{
		signIdentityFn: func(context.Context, *ca.SignRequest, bool) ([]*x509.Certificate, error) {
			return nil, nil
		},
		trustAnchorsFn: func() []byte {
			return nil
		},
	}
}

func (f *Fake) WithSignIdentity(fn func(context.Context, *ca.SignRequest, bool) ([]*x509.Certificate, error)) *Fake {
	f.signIdentityFn = fn
	return f
}

func (f *Fake) WithTrustAnchors(fn func() []byte) *Fake {
	f.trustAnchorsFn = fn
	return f
}

func (f *Fake) SignIdentity(ctx context.Context, req *ca.SignRequest, overrideDuration bool) ([]*x509.Certificate, error) {
	return f.signIdentityFn(ctx, req, overrideDuration)
}

func (f *Fake) TrustAnchors() []byte {
	return f.trustAnchorsFn()
}
