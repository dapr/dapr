package grpc

import (
	"context"
	"errors"

	"github.com/actionscore/actions/pkg/actors"

	"github.com/golang/protobuf/ptypes/any"
	"github.com/golang/protobuf/ptypes/empty"

	components_v1alpha1 "github.com/actionscore/actions/pkg/apis/components/v1alpha1"
	"github.com/actionscore/actions/pkg/channel"
	"github.com/actionscore/actions/pkg/components"
	"github.com/actionscore/actions/pkg/messaging"
	pb "github.com/actionscore/actions/pkg/proto"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// API is the gRPC interface for the Actions runtime
type API interface {
	CallActor(ctx context.Context, in *pb.CallActorEnvelope) (*pb.InvokeResponse, error)
	CallRemoteApp(ctx context.Context, in *pb.CallRemoteAppEnvelope) (*pb.InvokeResponse, error)
	CallLocal(ctx context.Context, in *pb.LocalCallEnvelope) (*pb.InvokeResponse, error)
	UpdateComponent(ctx context.Context, in *pb.Component) (*empty.Empty, error)
}

type api struct {
	appChannel        channel.AppChannel
	actor             actors.Actors
	directMessaging   messaging.DirectMessaging
	componentsHandler components.ComponentHandler
	id                string
}

// NewAPI returns a new gRPC API
func NewAPI(actionsID string, appChannel channel.AppChannel, directMessaging messaging.DirectMessaging, actor actors.Actors, componentHandler components.ComponentHandler) API {
	return &api{
		appChannel:        appChannel,
		directMessaging:   directMessaging,
		componentsHandler: componentHandler,
		actor:             actor,
		id:                actionsID,
	}
}

// CallRemoteApp invokes a remote app
func (a *api) CallRemoteApp(ctx context.Context, in *pb.CallRemoteAppEnvelope) (*pb.InvokeResponse, error) {
	req := messaging.DirectMessageRequest{
		Data:     in.Data.Value,
		Method:   in.Method,
		Metadata: in.Metadata,
		Target:   in.Target,
	}

	resp, err := a.directMessaging.Invoke(&req)
	if err != nil {
		return nil, err
	}

	return &pb.InvokeResponse{
		Data:     &any.Any{Value: resp.Data},
		Metadata: resp.Metadata,
	}, nil
}

// CallLocal is used for internal Actions-Actions calls
func (a *api) CallLocal(ctx context.Context, in *pb.LocalCallEnvelope) (*pb.InvokeResponse, error) {
	if a.appChannel == nil {
		return nil, errors.New("app channel is not initialized")
	}

	req := channel.InvokeRequest{
		Payload:  in.Data.Value,
		Method:   in.Method,
		Metadata: in.Metadata,
	}

	resp, err := a.appChannel.InvokeMethod(&req)
	if err != nil {
		return nil, err
	}

	return &pb.InvokeResponse{
		Data:     &any.Any{Value: resp.Data},
		Metadata: resp.Metadata,
	}, nil
}

// CallActor invokes a virtual actor
func (a *api) CallActor(ctx context.Context, in *pb.CallActorEnvelope) (*pb.InvokeResponse, error) {
	req := actors.CallRequest{
		ActorType: in.ActorType,
		ActorID:   in.ActorID,
		Data:      in.Data.Value,
		Method:    in.Method,
	}

	resp, err := a.actor.Call(&req)
	if err != nil {
		return nil, err
	}

	return &pb.InvokeResponse{
		Data:     &any.Any{Value: resp.Data},
		Metadata: map[string]string{},
	}, nil
}

// UpdateComponent is fired by the Actions control plane when a component state changes
func (a *api) UpdateComponent(ctx context.Context, in *pb.Component) (*empty.Empty, error) {
	c := components_v1alpha1.Component{
		ObjectMeta: meta_v1.ObjectMeta{
			Name: in.Metadata.Name,
		},
		Auth: components_v1alpha1.Auth{
			SecretStore: in.Auth.SecretStore,
		},
	}

	for _, m := range in.Spec.Metadata {
		c.Spec.Metadata = append(c.Spec.Metadata, components_v1alpha1.MetadataItem{
			Name:  m.Name,
			Value: m.Value,
			SecretKeyRef: components_v1alpha1.SecretKeyRef{
				Key:  m.SecretKeyRef.Key,
				Name: m.SecretKeyRef.Name,
			},
		})
	}

	a.componentsHandler.OnComponentUpdated(c)
	return &empty.Empty{}, nil
}
