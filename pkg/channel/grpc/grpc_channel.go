package grpc

import (
	"context"
	"fmt"
	"time"

	"github.com/golang/protobuf/ptypes/any"
	"google.golang.org/grpc"

	"github.com/actionscore/actions/pkg/channel"
	pb "github.com/actionscore/actions/pkg/proto"
)

type GRPCChannel struct {
	client      *grpc.ClientConn
	baseAddress string
}

func CreateLocalChannel(port int, conn *grpc.ClientConn) *GRPCChannel {
	return &GRPCChannel{
		client:      conn,
		baseAddress: fmt.Sprintf("127.0.0.1:%v", port),
	}
}

func (g *GRPCChannel) InvokeMethod(req *channel.InvokeRequest) (*channel.InvokeResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute*1)
	defer cancel()

	c := pb.NewAppClient(g.client)
	msg := pb.AppMethodCallEnvelope{
		Data:   &any.Any{Value: req.Payload},
		Method: req.Method,
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
