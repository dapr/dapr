// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package grpc

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/dapr/dapr/pkg/channel"
	"github.com/dapr/dapr/pkg/config"
	tracing "github.com/dapr/dapr/pkg/diagnostics"
	daprclient_pb "github.com/dapr/dapr/pkg/proto/daprclient"
	"github.com/golang/protobuf/ptypes/any"
	"google.golang.org/grpc"
)

// Channel is a concrete AppChannel implementation for interacting with gRPC based user code
type Channel struct {
	client      *grpc.ClientConn
	baseAddress string
	ch          chan int
	tracingSpec config.TracingSpec
}

// CreateLocalChannel creates a gRPC connection with user code
func CreateLocalChannel(port, maxConcurrency int, conn *grpc.ClientConn, spec config.TracingSpec) *Channel {
	c := &Channel{
		client:      conn,
		baseAddress: fmt.Sprintf("127.0.0.1:%v", port),
		tracingSpec: spec,
	}
	if maxConcurrency > 0 {
		c.ch = make(chan int, maxConcurrency)
	}
	return c
}

const (
	// QueryString is the query string passed by the request
	QueryString = "http.query_string"
)

// GetBaseAddress returns the application base address
func (g *Channel) GetBaseAddress() string {
	return g.baseAddress
}

// InvokeMethod invokes user code via gRPC
func (g *Channel) InvokeMethod(req *channel.InvokeRequest) (*channel.InvokeResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute*1)
	defer cancel()

	metadata, err := getQueryStringFromMetadata(req)
	if err != nil {
		return nil, err
	}
	msg := daprclient_pb.InvokeEnvelope{
		Data:     &any.Any{Value: req.Payload},
		Method:   req.Method,
		Metadata: metadata,
	}

	if g.ch != nil {
		g.ch <- 1
	}
	c := daprclient_pb.NewDaprClientClient(g.client)

	var span tracing.TracerSpan
	var spanc tracing.TracerSpan

	span, spanc = tracing.TracingSpanFromGRPCContext(ctx, nil, req.Method, g.tracingSpec)
	defer span.Span.End()
	defer spanc.Span.End()

	resp, err := c.OnInvoke(ctx, &msg)

	tracing.UpdateSpanPairStatusesFromError(span, spanc, err, req.Method)

	if g.ch != nil {
		<-g.ch
	}
	if err != nil {
		return nil, err
	}

	return &channel.InvokeResponse{
		Data:     resp.Value,
		Metadata: map[string]string{},
	}, nil
}

func getQueryStringFromMetadata(req *channel.InvokeRequest) (map[string]string, error) {
	var metadata map[string]string
	if val, ok := req.Metadata[QueryString]; ok {
		metadata = make(map[string]string)
		params, err := url.ParseQuery(val)
		if err != nil {
			return nil, err
		}
		for k, v := range params {
			if len(v) != 1 {
				metadata[k] = strings.Join(v, ",")
			} else {
				metadata[k] = v[0]
			}
		}
	}
	return metadata, nil
}
