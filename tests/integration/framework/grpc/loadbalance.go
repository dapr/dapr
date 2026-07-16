/*
Copyright 2025 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://wwb.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package grpc

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

type loadbalance struct {
	conns []grpc.ClientConnInterface
	idx   atomic.Uint64
}

func LoadBalance(t *testing.T, conns ...grpc.ClientConnInterface) grpc.ClientConnInterface {
	require.NotEmpty(t, conns)
	return &loadbalance{
		conns: conns,
	}
}

func (l *loadbalance) Invoke(ctx context.Context, method string, args any, reply any, opts ...grpc.CallOption) error {
	return l.conns[l.idx.Add(1)%uint64(len(l.conns))].Invoke(ctx, method, args, reply, opts...)
}

func (l *loadbalance) NewStream(ctx context.Context, desc *grpc.StreamDesc, method string, opts ...grpc.CallOption) (grpc.ClientStream, error) {
	return l.conns[l.idx.Add(1)%uint64(len(l.conns))].NewStream(ctx, desc, method, opts...)
}
