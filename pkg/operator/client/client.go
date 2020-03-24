package client

import (
	diag "github.com/dapr/dapr/pkg/diagnostics"
	pb "github.com/dapr/dapr/pkg/proto/operator"
	"google.golang.org/grpc"
)

// GetOperatorClient returns a new k8s operator client and the underlying connection
func GetOperatorClient(address string) (pb.OperatorClient, *grpc.ClientConn, error) {
	conn, err := grpc.Dial(address, grpc.WithInsecure(), grpc.WithStatsHandler(diag.DefaultGRPCMonitoring.ClientStatsHandler))
	if err != nil {
		return nil, nil, err
	}
	return pb.NewOperatorClient(conn), conn, nil
}
