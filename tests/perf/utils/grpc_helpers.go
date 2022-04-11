package utils

import (
	"context"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/anypb"

	v1 "github.com/dapr/dapr/pkg/proto/common/v1"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
)

type GrpcAccessFunction = func(cc *grpc.ClientConn) ([]byte, error)

// GrpcAccessNTimes calls the method n times and returns the first success or last error.
func GrpcAccessNTimes(address string, f GrpcAccessFunction, n int) ([]byte, error) {
	var res []byte
	var err error
	for i := n - 1; i >= 0; i-- {
		res, err = GrpcAccess(address, f)
		if i == 0 {
			break
		}

		if err != nil {
			println(err.Error())
			time.Sleep(time.Second)
		} else {
			return res, nil
		}
	}

	return res, err
}

// GrpcAccess is a helper to make gRPC call to the method.
func GrpcAccess(address string, f GrpcAccessFunction) ([]byte, error) {
	cc, error := grpc.Dial(address, grpc.WithInsecure())
	if error != nil {
		return nil, error
	}

	return f(cc)
}

// GrpcServiceInvoke call OnInvoke() of dapr AppCallback.
func GrpcServiceInvoke(cc *grpc.ClientConn) ([]byte, error) {
	clientV1 := runtimev1pb.NewAppCallbackClient(cc)

	response, error := clientV1.OnInvoke(context.Background(), &v1.InvokeRequest{
		Method: "load",
		Data:   &anypb.Any{Value: []byte("")},
	})
	if error != nil {
		return nil, error
	}

	if response.GetData() == nil {
		return nil, nil
	}

	return response.GetData().Value, nil
}
