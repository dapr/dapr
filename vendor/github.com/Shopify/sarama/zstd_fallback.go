// +build !cgo

package sarama

import (
	"errors"
)

var errZstdCgo = errors.New("zstd compression requires building with cgo enabled")

func zstdDecompress(dst, src []byte) ([]byte, error) {
	return nil, errZstdCgo
}

func zstdCompressLevel(dst, src []byte, level int) ([]byte, error) {
	return nil, errZstdCgo
}
