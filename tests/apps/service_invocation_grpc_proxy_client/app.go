// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"google.golang.org/grpc"
	pb "google.golang.org/grpc/examples/helloworld/helloworld"
	"google.golang.org/grpc/metadata"
)

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
	w.WriteHeader(200)
	w.Write([]byte("success"))
}

func main() {
	http.HandleFunc("/tests/invoke_test", run)
	log.Fatal(http.ListenAndServe(":3000", nil))
}
