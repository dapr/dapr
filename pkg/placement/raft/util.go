// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package raft

import (
	"bytes"
	"os"

	"github.com/hashicorp/go-msgpack/codec"
	"github.com/pkg/errors"
)

const defaultDirPermission = 0755

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
