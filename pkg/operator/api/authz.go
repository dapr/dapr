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

package api

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dapr/dapr/pkg/security/spiffe"
)

// authzRequest ensures that the requesting identity resides in the same
// namespace as that of the requested namespace.
func (a *apiServer) authzRequest(ctx context.Context, namespace string) error {
	spiffeID, ok, err := spiffe.FromGRPCContext(ctx)
	if err != nil || !ok {
		return status.New(codes.PermissionDenied, "failed to determine identity").Err()
	}

	if len(namespace) == 0 || spiffeID.Namespace() != namespace {
		return status.New(codes.PermissionDenied, "identity does not match requested namespace").Err()
	}

	return nil
}
