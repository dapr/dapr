package grpc

import (
	"context"
	"net/http"

	"google.golang.org/grpc"

	"github.com/dapr/dapr/pkg/api/grpc/metadata"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
)

func getAPIAuthenticationMiddlewares(apiToken, authHeader string) (grpc.UnaryServerInterceptor, grpc.StreamServerInterceptor) {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
			err := checkAPITokenInContext(ctx, apiToken, authHeader)
			if err != nil {
				return nil, err
			}
			return handler(ctx, req)
		},
		func(srv any, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
			err := checkAPITokenInContext(stream.Context(), apiToken, authHeader)
			if err != nil {
				return err
			}
			return handler(srv, stream)
		}
}

// Checks if the API token in the gRPC request's context is valid; returns an error otherwise.
func checkAPITokenInContext(ctx context.Context, apiToken, authHeader string) error {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return invokev1.ErrorFromHTTPResponseCode(http.StatusUnauthorized, "missing metadata in request")
	}

	if len(md[authHeader]) == 0 {
		return invokev1.ErrorFromHTTPResponseCode(http.StatusUnauthorized, "missing api token in request metadata")
	}

	if md[authHeader][0] != apiToken {
		return invokev1.ErrorFromHTTPResponseCode(http.StatusUnauthorized, "authentication error: api token mismatch")
	}

	md.Set(authHeader, "")
	return nil
}
