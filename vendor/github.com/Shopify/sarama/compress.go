package sarama

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"sync"

	"github.com/eapache/go-xerial-snappy"
	"github.com/pierrec/lz4"
)

var (
	lz4WriterPool = sync.Pool{
		New: func() interface{} {
			return lz4.NewWriter(nil)
		},
	}

	gzipWriterPool = sync.Pool{
		New: func() interface{} {
			return gzip.NewWriter(nil)
		},
	}
)

func compress(cc CompressionCodec, level int, data []byte) ([]byte, error) {
	switch cc {
	case CompressionNone:
		return data, nil
	case CompressionGZIP:
		var (
			err    error
			buf    bytes.Buffer
			writer *gzip.Writer
		)
		if level != CompressionLevelDefault {
			writer, err = gzip.NewWriterLevel(&buf, level)
			if err != nil {
				return nil, err
			}
		} else {
			writer = gzipWriterPool.Get().(*gzip.Writer)
			defer gzipWriterPool.Put(writer)
			writer.Reset(&buf)
		}
		if _, err := writer.Write(data); err != nil {
			return nil, err
		}
		if err := writer.Close(); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	case CompressionSnappy:
		return snappy.Encode(data), nil
	case CompressionLZ4:
		writer := lz4WriterPool.Get().(*lz4.Writer)
		defer lz4WriterPool.Put(writer)

		var buf bytes.Buffer
		writer.Reset(&buf)

		if _, err := writer.Write(data); err != nil {
			return nil, err
		}
		if err := writer.Close(); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	case CompressionZSTD:
		return zstdCompressLevel(nil, data, level)
	default:
		return nil, PacketEncodingError{fmt.Sprintf("unsupported compression codec (%d)", cc)}
	}
}
