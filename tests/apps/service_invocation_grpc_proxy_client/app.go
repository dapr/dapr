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
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"google.golang.org/grpc"
	pb "google.golang.org/grpc/examples/helloworld/helloworld"
	"google.golang.org/grpc/metadata"
)

type appResponse struct {
	Message string `json:"message,omitempty"`
}

func run(w http.ResponseWriter, r *http.Request) {
	conn, err := grpc.Dial("localhost:50001", grpc.WithInsecure(), grpc.WithBlock())
	if err != nil {
		log.Fatalf("did not connect: %v", err)
	}
	defer conn.Close()
	c := pb.NewGreeterClient(conn)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*2)
	defer cancel()

	ctx = metadata.AppendToOutgoingContext(ctx, "dapr-app-id", "grpcproxyserver")
	resp, err := c.SayHello(ctx, &pb.HelloRequest{Name: "Darth Tyranus"})
	if err != nil {
		log.Printf("could not greet: %v\n", err)
		w.WriteHeader(500)
		w.Write([]byte(fmt.Sprintf("failed to proxy request: %s", err)))
		return
	}

	log.Printf("Greeting: %s", resp.GetMessage())

	appResp := appResponse{
		Message: "success",
	}

	b, err := json.Marshal(appResp)
	if err != nil {
		log.Fatal(err)
	}

	w.WriteHeader(200)
	w.Write(b)
}

func main() {
	http.HandleFunc("/tests/invoke_test", run)
	log.Fatal(http.ListenAndServe(":3000", nil))
}
