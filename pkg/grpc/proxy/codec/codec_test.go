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

// Based on https://github.com/trusch/grpc-proxy
// Copyright Michal Witkowski. Licensed under Apache2 license: https://github.com/trusch/grpc-proxy/blob/master/LICENSE.txt

package codec

import (
	"testing"

	_ "github.com/gogo/protobuf/proto"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/encoding"

	pb "github.com/dapr/dapr/pkg/grpc/proxy/testservice"
)

func TestCodec_ReadYourWrites(t *testing.T) {
	framePtr := &Frame{}
	data := []byte{0xDE, 0xAD, 0xBE, 0xEF}
	Register()
	codec := encoding.GetCodec((&Proxy{}).Name())
	require.NotNil(t, codec, "codec must be registered")
	require.NoError(t, codec.Unmarshal(data, framePtr), "unmarshalling must go ok")
	out, err := codec.Marshal(framePtr)
	require.NoError(t, err, "no marshal error")
	require.Equal(t, data, out, "output and data must be the same")

	// reuse
	require.NoError(t, codec.Unmarshal([]byte{0x55}, framePtr), "unmarshalling must go ok")
	out, err = codec.Marshal(framePtr)
	require.NoError(t, err, "no marshal error")
	require.Equal(t, []byte{0x55}, out, "output and data must be the same")
}

func TestProtoCodec_ReadYourWrites(t *testing.T) {
	p1 := &pb.PingRequest{
		Value: "test-ping",
	}
	proxyCd := encoding.GetCodec((&Proxy{}).Name())

	require.NotNil(t, proxyCd, "proxy codec must not be nil")

	out1p1, err := proxyCd.Marshal(p1)
	require.NoError(t, err, "marshalling must go ok")
	out2p1, err := proxyCd.Marshal(p1)
	require.NoError(t, err, "marshalling must go ok")

	p2 := &pb.PingRequest{}
	err = proxyCd.Unmarshal(out1p1, p2)
	require.NoError(t, err, "unmarshalling must go ok")
	err = proxyCd.Unmarshal(out2p1, p2)
	require.NoError(t, err, "unmarshalling must go ok")

	require.Equal(t, p1.ProtoReflect(), p2.ProtoReflect())
	require.Equal(t, p1.Value, p2.Value)
}
