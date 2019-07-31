package messaging

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/golang/protobuf/ptypes/any"

	"github.com/actionscore/actions/pkg/modes"

	"github.com/actionscore/actions/pkg/channel"
	"google.golang.org/grpc"

	pb "github.com/actionscore/actions/pkg/proto"
)

// DirectMessaging is the API interface for invoking a remote app
type DirectMessaging interface {
	Invoke(req *DirectMessageRequest) (*DirectMessageResponse, error)
}

type directMessaging struct {
	appChannel          channel.AppChannel
	connectionCreatorFn func(address string) (*grpc.ClientConn, error)
	actionsID           string
	mode                modes.ActionsMode
	grpcPort            int
	namespace           string
}

// NewDirectMessaging returns a new direct messaging api
func NewDirectMessaging(actionsID, namespace string, port int, mode modes.ActionsMode, appChannel channel.AppChannel, grpcConnectionFn func(address string) (*grpc.ClientConn, error)) DirectMessaging {
	return &directMessaging{
		appChannel:          appChannel,
		connectionCreatorFn: grpcConnectionFn,
		actionsID:           actionsID,
		mode:                mode,
		grpcPort:            port,
		namespace:           namespace,
	}
}

// Invoke takes a message requests and invokes an app, either local or remote
func (d *directMessaging) Invoke(req *DirectMessageRequest) (*DirectMessageResponse, error) {
	var invokeFn func(*DirectMessageRequest) (*DirectMessageResponse, error)

	if req.Target == d.actionsID {
		invokeFn = d.invokeLocal
	} else {
		invokeFn = d.invokeRemote
	}

	return invokeFn(req)
}

func (d *directMessaging) invokeLocal(req *DirectMessageRequest) (*DirectMessageResponse, error) {
	if d.appChannel == nil {
		return nil, errors.New("cannot invoke local endpoint: app channel not initialized")
	}

	localInvokeReq := channel.InvokeRequest{
		Metadata: req.Metadata,
		Method:   req.Method,
		Payload:  req.Data,
	}

	resp, err := d.appChannel.InvokeMethod(&localInvokeReq)
	if err != nil {
		return nil, err
	}

	return &DirectMessageResponse{
		Data:     resp.Data,
		Metadata: resp.Metadata,
	}, nil
}

func (d *directMessaging) invokeRemote(req *DirectMessageRequest) (*DirectMessageResponse, error) {
	address, err := d.getAddress(req.Target)
	if err != nil {
		return nil, err
	}

	conn, err := d.connectionCreatorFn(address)
	if err != nil {
		return nil, err
	}

	msg := pb.LocalCallEnvelope{
		Data:     &any.Any{Value: req.Data},
		Metadata: req.Metadata,
		Method:   req.Method,
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute*1)
	defer cancel()

	client := pb.NewActionsClient(conn)
	resp, err := client.CallLocal(ctx, &msg)
	if err != nil {
		return nil, err
	}

	return &DirectMessageResponse{
		Data:     resp.Data.Value,
		Metadata: resp.Metadata,
	}, nil
}

func (d *directMessaging) getAddress(target string) (string, error) {
	switch d.mode {
	case modes.KubernetesMode:
		return fmt.Sprintf("%s-action.%s.svc.cluster.local:%v", target, d.namespace, d.grpcPort), nil
	default:
		return "", fmt.Errorf("remote calls not supported for %s mode", string(d.mode))
	}
}
