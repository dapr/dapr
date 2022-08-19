/*
Copyright 2022 The Dapr Authors
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
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
)

// GetGRPCClient returns a gRPC client to connect to Dapr
func GetGRPCClient(daprPort int) runtimev1pb.DaprClient {
	if daprPort == 0 {
		if s, _ := os.LookupEnv("DAPR_GRPC_PORT"); s != "" {
			daprPort, _ = strconv.Atoi(s)
		}
	}
	url := fmt.Sprintf("localhost:%d", daprPort)
	log.Printf("Connecting to dapr using url %s", url)

	var grpcConn *grpc.ClientConn
	start := time.Now()
	for retries := 10; retries > 0; retries-- {
		var err error
		grpcConn, err = grpc.Dial(url,
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithBlock(),
		)
		if err == nil {
			break
		}

		if retries == 0 {
			log.Printf("Could not connect to dapr: %v", err)
			log.Panic(err)
		}

		log.Printf("Could not connect to dapr: %v, retrying...", err)
		time.Sleep(5 * time.Second)
	}

	elapsed := time.Since(start)
	log.Printf("gRPC connect elapsed: %v", elapsed)
	return runtimev1pb.NewDaprClient(grpcConn)
}
