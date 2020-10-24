package main

import (
	"context"
	"log"

	"github.com/dapr/go-sdk/service/common"
	daprd "github.com/dapr/go-sdk/service/grpc"
)

func main() {
	s, err := daprd.NewService(":50001")

	if err != nil {
		log.Fatalf("failed to start server on port 50001: %v", err)
	}

	if err := s.AddServiceInvocationHandler("echo", runHandler); err != nil {
		log.Fatalf("failed to add service invocation handler: %v", err)
	}

	if err := s.Start(); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}

func runHandler(ctx context.Context, e *common.InvocationEvent) (out *common.Content, err error) {
	out = &common.Content{
		Data:        []byte("ok"),
		ContentType: "text/plain",
	}
	return
}
