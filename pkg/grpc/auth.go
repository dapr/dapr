package grpc

import (
	"context"
	"net/http"

	"google.golang.org/grpc"

	"github.com/dapr/dapr/pkg/grpc/metadata"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
)

func setAPIAuthenticationMiddlewareUnary(apiToken, authHeader string) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		md, ok := metadata.FromIncomingContext(ctx)
		if !ok {
			err := invokev1.ErrorFromHTTPResponseCode(http.StatusUnauthorized, "missing metadata in request")
			return nil, err
		}

		token, ok := md[authHeader]
		if !ok || len(token) == 0 {
			err := invokev1.ErrorFromHTTPResponseCode(http.StatusUnauthorized, "missing api token in request metadata")
			return nil, err
		}

		if token[0] != apiToken {
			err := invokev1.ErrorFromHTTPResponseCode(http.StatusUnauthorized, "authentication error: api token mismatch")
			return nil, err
		}

		md.Set(authHeader, "")
		return handler(ctx, req)
	}
}
