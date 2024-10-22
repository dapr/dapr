package grpc

import (
	"context"
	"net/http"

	"google.golang.org/grpc"
	grpc_metadata "google.golang.org/grpc/metadata"

	"github.com/dapr/dapr/pkg/api/grpc/metadata"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
)

type wrappedStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *wrappedStream) Context() context.Context {
	return s.ctx
}

func getAPIAuthenticationMiddlewares(apiToken, authHeader string) (grpc.UnaryServerInterceptor, grpc.StreamServerInterceptor) {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
			authCtx, err := checkAPITokenInContext(ctx, apiToken, authHeader)
			if err != nil {
				return nil, err
			}
			return handler(authCtx, req)
		},
		func(srv any, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
			authCtx, err := checkAPITokenInContext(stream.Context(), apiToken, authHeader)
			if err != nil {
				return err
			}

			return handler(srv, &wrappedStream{stream, authCtx})
		}
}

// Checks if the API token in the gRPC request's context is valid; returns an error otherwise.
func checkAPITokenInContext(ctx context.Context, apiToken, authHeader string) (context.Context, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ctx, invokev1.ErrorFromHTTPResponseCode(http.StatusUnauthorized, "missing metadata in request")
	}

	if len(md[authHeader]) == 0 {
		return ctx, invokev1.ErrorFromHTTPResponseCode(http.StatusUnauthorized, "missing api token in request metadata")
	}

	if md[authHeader][0] != apiToken {
		return ctx, invokev1.ErrorFromHTTPResponseCode(http.StatusUnauthorized, "authentication error: api token mismatch")
	}

	md.Delete(authHeader)
	ctx = grpc_metadata.NewIncomingContext(ctx, md.Copy())
	return ctx, nil
}
