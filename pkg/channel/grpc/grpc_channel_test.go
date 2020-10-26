// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
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

	channelt "github.com/dapr/dapr/pkg/channel/testing"
	"github.com/dapr/dapr/pkg/config"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc"
)

// TODO: Add APIVersion testing

func TestCreateChannel(t *testing.T) {
	t.Run("Test empty application host", func(t *testing.T) {
		c := CreateChannel(9999, 1, nil, config.TracingSpec{}, "")
		assert.Equal(t, ":9999", c.baseAddress)
		close(c.ch)
	})

	t.Run("Test non-empty application host", func(t *testing.T) {
		c := CreateChannel(9999, 1, nil, config.TracingSpec{}, "10.1.1.2")
		assert.Equal(t, "10.1.1.2:9999", c.baseAddress)
		close(c.ch)
	})
}

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
	defer klose(t, conn)
	assert.NoError(t, err)

	c := Channel{baseAddress: "localhost:9998", client: conn}
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
	assert.Equal(t, "{\"param1\":\"val1\",\"param2\":\"val2\"}", actual["querystring"])
}

func klose(t *testing.T, c io.Closer) {
	err := c.Close()
	if err != nil {
		assert.Fail(t, fmt.Sprintf("unable to close %s", err))
	}
}
