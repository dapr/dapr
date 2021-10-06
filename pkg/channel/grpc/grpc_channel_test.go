// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package grpc

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc"

	channelt "github.com/dapr/dapr/pkg/channel/testing"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	auth "github.com/dapr/dapr/pkg/runtime/security"
)

// TODO: Add APIVersion testing

func TestInvokeMethod(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:9998")
	assert.NoError(t, err)

	grpcServer := grpc.NewServer()
	go func() {
		runtimev1pb.RegisterAppCallbackServer(grpcServer, &channelt.MockServer{})
		grpcServer.Serve(lis)
	}()

	var opts []grpc.DialOption
	opts = append(opts, grpc.WithInsecure())
	conn, err := grpc.Dial("localhost:9998", opts...)
	defer close(t, conn)
	assert.NoError(t, err)

	c := Channel{baseAddress: "localhost:9998", client: conn, appMetadataToken: "token1", maxRequestBodySize: 4, readBufferSize: 4}
	req := invokev1.NewInvokeMethodRequest("method")
	req.WithHTTPExtension(http.MethodPost, "param1=val1&param2=val2")
	response, err := c.InvokeMethod(context.Background(), req)
	assert.NoError(t, err)
	contentType, body := response.RawData()
	grpcServer.Stop()

	assert.Equal(t, "application/json", contentType)

	actual := map[string]string{}
	json.Unmarshal(body, &actual)

	assert.Equal(t, "POST", actual["httpverb"])
	assert.Equal(t, "method", actual["method"])
	assert.Equal(t, "token1", actual[auth.APITokenHeader])
	assert.Equal(t, "param1=val1&param2=val2", actual["querystring"])
}

func close(t *testing.T, c io.Closer) {
	err := c.Close()
	if err != nil {
		assert.Fail(t, fmt.Sprintf("unable to close %s", err))
	}
}
