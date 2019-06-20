// +build cgo

package sarama

import "github.com/DataDog/zstd"

func zstdDecompress(dst, src []byte) ([]byte, error) {
	return zstd.Decompress(dst, src)
}

func zstdCompressLevel(dst, src []byte, level int) ([]byte, error) {
	return zstd.CompressLevel(dst, src, level)
}
