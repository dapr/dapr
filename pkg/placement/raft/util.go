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

package raft

import (
	"bytes"
	"errors"
	"os"

	"github.com/hashicorp/go-msgpack/v2/codec"
)

const defaultDirPermission = 0o755

func ensureDir(dirName string) error {
	info, err := os.Stat(dirName)
	if !os.IsNotExist(err) && !info.Mode().IsDir() {
		return errors.New("file already existed")
	}

	err = os.Mkdir(dirName, defaultDirPermission)
	if err == nil || os.IsExist(err) {
		return nil
	}
	return err
}

func makeRaftLogCommand(t CommandType, member DaprHostMember) ([]byte, error) {
	buf := bytes.NewBuffer(nil)
	buf.WriteByte(uint8(t))
	err := codec.NewEncoder(buf, &codec.MsgpackHandle{}).Encode(member)
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func marshalMsgPack(in interface{}) ([]byte, error) {
	buf := bytes.NewBuffer(nil)
	enc := codec.NewEncoder(buf, &codec.MsgpackHandle{})
	err := enc.Encode(in)
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func unmarshalMsgPack(in []byte, out interface{}) error {
	dec := codec.NewDecoderBytes(in, &codec.MsgpackHandle{})
	return dec.Decode(out)
}

func raftAddressForID(id string, nodes []PeerInfo) string {
	for _, node := range nodes {
		if node.ID == id {
			return node.Address
		}
	}

	return ""
}
