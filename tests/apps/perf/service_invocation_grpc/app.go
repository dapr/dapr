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

package main

import (
	"context"
	"log"

	"github.com/dapr/go-sdk/service/common"
	daprd "github.com/dapr/go-sdk/service/grpc"
)

func main() {
	s, err := daprd.NewService(":3000")
	if err != nil {
		log.Fatalf("failed to start the server: %v", err)
	}

	if err := s.AddServiceInvocationHandler("load", loadTestHandle); err != nil {
		log.Fatalf("error adding invocation handler: %v", err)
	}

	if err := s.Start(); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

func loadTestHandle(ctx context.Context, in *common.InvocationEvent) (*common.Content, error) {
	out := &common.Content{
		Data:        []byte("response"),
		ContentType: "text/plain",
	}
	return out, nil
}
