/*
Copyright 2025 The Dapr Authors
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

package grpc

import (
	"context"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(basic))
}

type basic struct {
	daprd *daprd.Daprd
}

const componentYAML = `
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: mystore
spec:
  type: binarystore.fake
  version: v1
`

func (b *basic) Setup(t *testing.T) []framework.Option {
	b.daprd = daprd.New(t, daprd.WithResourceFiles(componentYAML))
	return []framework.Option{
		framework.WithProcesses(b.daprd),
	}
}

func (b *basic) Run(t *testing.T, ctx context.Context) {
	b.daprd.WaitUntilRunning(t, ctx)
	client := b.daprd.GRPCClient(t, ctx)

	t.Run("set (overwrite) then get round-trips", func(t *testing.T) {
		stream, err := client.SetBinaryFileAlpha1(ctx)
		require.NoError(t, err)

		require.NoError(t, stream.Send(&rtv1.SetBinaryFileRequest{
			SetBinaryFileRequestType: &rtv1.SetBinaryFileRequest_Options{
				Options: &rtv1.SetBinaryFileRequestOptions{
					ComponentName: "mystore",
					FileName:      "hello.bin",
					Overwrite:     true,
				},
			},
		}))
		require.NoError(t, stream.Send(&rtv1.SetBinaryFileRequest{
			SetBinaryFileRequestType: &rtv1.SetBinaryFileRequest_Payload{
				Payload: &commonv1pb.StreamPayload{Data: []byte("hello world"), Seq: 0},
			},
		}))
		_, err = stream.CloseAndRecv()
		require.NoError(t, err)

		getStream, err := client.GetBinaryFileAlpha1(ctx, &rtv1.GetBinaryFileRequest{
			ComponentName: "mystore",
			FileName:      "hello.bin",
		})
		require.NoError(t, err)

		var got []byte
		for {
			msg, err := getStream.Recv()
			if err == io.EOF {
				break
			}
			require.NoError(t, err)
			got = append(got, msg.GetPayload().GetData()...)
		}
		assert.Equal(t, []byte("hello world"), got)
	})

	t.Run("set without overwrite conflicts", func(t *testing.T) {
		// "hello.bin" already exists from the previous subtest.
		stream, err := client.SetBinaryFileAlpha1(ctx)
		require.NoError(t, err)
		require.NoError(t, stream.Send(&rtv1.SetBinaryFileRequest{
			SetBinaryFileRequestType: &rtv1.SetBinaryFileRequest_Options{
				Options: &rtv1.SetBinaryFileRequestOptions{
					ComponentName: "mystore",
					FileName:      "hello.bin",
					Overwrite:     false,
				},
			},
		}))
		require.NoError(t, stream.Send(&rtv1.SetBinaryFileRequest{
			SetBinaryFileRequestType: &rtv1.SetBinaryFileRequest_Payload{
				Payload: &commonv1pb.StreamPayload{Data: []byte("second"), Seq: 0},
			},
		}))
		_, err = stream.CloseAndRecv()
		require.Error(t, err)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.AlreadyExists, st.Code())
	})

	t.Run("get missing file returns NotFound", func(t *testing.T) {
		getStream, err := client.GetBinaryFileAlpha1(ctx, &rtv1.GetBinaryFileRequest{
			ComponentName: "mystore",
			FileName:      "missing.bin",
		})
		require.NoError(t, err)
		_, err = getStream.Recv()
		require.Error(t, err)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.NotFound, st.Code())
	})

	t.Run("delete then get returns NotFound", func(t *testing.T) {
		stream, err := client.SetBinaryFileAlpha1(ctx)
		require.NoError(t, err)
		require.NoError(t, stream.Send(&rtv1.SetBinaryFileRequest{
			SetBinaryFileRequestType: &rtv1.SetBinaryFileRequest_Options{
				Options: &rtv1.SetBinaryFileRequestOptions{
					ComponentName: "mystore",
					FileName:      "temp.bin",
					Overwrite:     true,
				},
			},
		}))
		require.NoError(t, stream.Send(&rtv1.SetBinaryFileRequest{
			SetBinaryFileRequestType: &rtv1.SetBinaryFileRequest_Payload{
				Payload: &commonv1pb.StreamPayload{Data: []byte("temp"), Seq: 0},
			},
		}))
		_, err = stream.CloseAndRecv()
		require.NoError(t, err)

		_, err = client.DeleteBinaryFileAlpha1(ctx, &rtv1.DeleteBinaryFileRequest{
			ComponentName: "mystore",
			FileName:      "temp.bin",
		})
		require.NoError(t, err)

		getStream, err := client.GetBinaryFileAlpha1(ctx, &rtv1.GetBinaryFileRequest{
			ComponentName: "mystore",
			FileName:      "temp.bin",
		})
		require.NoError(t, err)
		_, err = getStream.Recv()
		require.Error(t, err)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.NotFound, st.Code())
	})

	t.Run("component not found returns InvalidArgument", func(t *testing.T) {
		_, err := client.DeleteBinaryFileAlpha1(ctx, &rtv1.DeleteBinaryFileRequest{
			ComponentName: "does-not-exist",
			FileName:      "x.bin",
		})
		require.Error(t, err)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.InvalidArgument, st.Code())
	})
}
