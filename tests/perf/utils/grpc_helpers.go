//go:build perf
// +build perf

/*
Copyright 2021 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package utils

import (
	"context"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
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
	cc, error := grpc.Dial(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
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
