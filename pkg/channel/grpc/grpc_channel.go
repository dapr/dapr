package grpc

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/golang/protobuf/ptypes/any"
	"google.golang.org/grpc"

	"github.com/actionscore/actions/pkg/channel"
	pb "github.com/actionscore/actions/pkg/proto"
)

// Channel is a concrete AppChannel implementation for interacting with gRPC based user code
type Channel struct {
	client      *grpc.ClientConn
	baseAddress string
}

// CreateLocalChannel creates a gRPC connection with user code
func CreateLocalChannel(port int, conn *grpc.ClientConn) *Channel {
	return &Channel{
		client:      conn,
		baseAddress: fmt.Sprintf("127.0.0.1:%v", port),
	}
}

const (
	// QueryString is the query string passed by the request
	QueryString = "http.query_string"
)

// InvokeMethod invokes user code via gRPC
func (g *Channel) InvokeMethod(req *channel.InvokeRequest) (*channel.InvokeResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute*1)
	defer cancel()

	c := pb.NewAppClient(g.client)

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
	msg := pb.AppMethodCallEnvelope{
		Data:     &any.Any{Value: req.Payload},
		Method:   req.Method,
		Metadata: metadata,
	}

	resp, err := c.OnMethodCall(ctx, &msg)
	if err != nil {
		return nil, err
	}

	return &channel.InvokeResponse{
		Data:     resp.Value,
		Metadata: map[string]string{},
	}, nil
}
