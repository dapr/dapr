package grpc

import (
	"fmt"
	"sync"

	"google.golang.org/grpc"

	"github.com/actionscore/actions/pkg/channel"
	grpc_channel "github.com/actionscore/actions/pkg/channel/grpc"
)

type GRPCManager struct {
	AppClient      *grpc.ClientConn
	lock           *sync.Mutex
	connectionPool map[string]*grpc.ClientConn
}

func NewGRPCManager() *GRPCManager {
	return &GRPCManager{
		lock:           &sync.Mutex{},
		connectionPool: map[string]*grpc.ClientConn{},
	}
}

func (g *GRPCManager) CreateLocalChannel(port int) (channel.AppChannel, error) {
	conn, err := g.GetGRPCConnection(fmt.Sprintf("127.0.0.1:%v", port))
	if err != nil {
		return nil, fmt.Errorf("error establishing connection to app grpc on port %v: %s", port, err)
	}

	g.AppClient = conn
	ch := grpc_channel.CreateLocalChannel(port, conn)
	return ch, nil
}

func (g *GRPCManager) GetGRPCConnection(address string) (*grpc.ClientConn, error) {
	if val, ok := g.connectionPool[address]; ok {
		return val, nil
	}

	g.lock.Lock()
	if val, ok := g.connectionPool[address]; ok {
		g.lock.Unlock()
		return val, nil
	}

	conn, err := grpc.Dial(address, grpc.WithInsecure())
	if err != nil {
		g.lock.Unlock()
		return nil, err
	}

	g.connectionPool[address] = conn
	g.lock.Unlock()

	return conn, nil
}
