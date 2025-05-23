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

	"github.com/lestrrat-go/jwx/v2/jwa"
	"github.com/lestrrat-go/jwx/v2/jwk"

	"github.com/dapr/dapr/pkg/sentry/server/ca"
	"github.com/dapr/dapr/pkg/sentry/server/ca/jwt"
)

type Fake struct {
	signIdentityFn        func(context.Context, *ca.SignRequest) ([]*x509.Certificate, error)
	trustAnchorsFn        func() []byte
	generateJWTFn         func(context.Context, *jwt.Request) (string, error)
	jwksFn                func() jwk.Set
	jwtSignatureAlgorithm func() jwa.KeyAlgorithm
}

func New() *Fake {
	return &Fake{
		signIdentityFn: func(context.Context, *ca.SignRequest) ([]*x509.Certificate, error) {
			return nil, nil
		},
		trustAnchorsFn: func() []byte {
			return nil
		},
		generateJWTFn: func(context.Context, *jwt.Request) (string, error) {
			return "", nil
		},
		jwksFn: func() jwk.Set {
			return nil
		},
		jwtSignatureAlgorithm: func() jwa.KeyAlgorithm {
			return jwa.PS256
		},
	}
}

func (f *Fake) WithSignIdentity(fn func(context.Context, *ca.SignRequest) ([]*x509.Certificate, error)) *Fake {
	f.signIdentityFn = fn
	return f
}

func (f *Fake) WithTrustAnchors(fn func() []byte) *Fake {
	f.trustAnchorsFn = fn
	return f
}

func (f *Fake) WithGenerateJWT(fn func(context.Context, *jwt.Request) (string, error)) *Fake {
	f.generateJWTFn = fn
	return f
}

func (f *Fake) WithJWKS(fn func() jwk.Set) *Fake {
	f.jwksFn = fn
	return f
}

func (f *Fake) WithJWTSignatureAlgorithm(fn func() jwa.KeyAlgorithm) *Fake {
	f.jwtSignatureAlgorithm = fn
	return f
}

func (f *Fake) SignIdentity(ctx context.Context, req *ca.SignRequest) ([]*x509.Certificate, error) {
	return f.signIdentityFn(ctx, req)
}

func (f *Fake) TrustAnchors() []byte {
	return f.trustAnchorsFn()
}

func (f *Fake) Generate(ctx context.Context, req *jwt.Request) (string, error) {
	return f.generateJWTFn(ctx, req)
}

func (f *Fake) JWKS() jwk.Set {
	return f.jwksFn()
}

func (f *Fake) JWTSignatureAlgorithm() jwa.KeyAlgorithm {
	return f.jwtSignatureAlgorithm()
}
