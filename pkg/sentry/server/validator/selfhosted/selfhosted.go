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

package selfhosted

import (
	"context"

	"github.com/spiffe/go-spiffe/v2/spiffeid"

	sentryv1pb "github.com/dapr/dapr/pkg/proto/sentry/v1"
	"github.com/dapr/dapr/pkg/sentry/server/validator"
	"github.com/dapr/dapr/pkg/sentry/server/validator/internal"
)

type selfhosted struct{}

func New() validator.Interface {
	return &selfhosted{}
}

func (s *selfhosted) Start(ctx context.Context) error {
	<-ctx.Done()
	return nil
}

func (s *selfhosted) Validate(ctx context.Context, req *sentryv1pb.SignCertificateRequest) (spiffeid.TrustDomain, error) {
	return internal.Validate(ctx, req)
}
