package fasthttp

import (
	"github.com/valyala/bytebufferpool"
)

// ByteBuffer provides byte buffer, which can be used with fasthttp API
// in order to minimize memory allocations.
//
// ByteBuffer may be used with functions appending data to the given []byte
// slice. See example code for details.
//
// Use AcquireByteBuffer for obtaining an empty byte buffer.
//
// ByteBuffer is deprecated. Use github.com/valyala/bytebufferpool instead.
type ByteBuffer bytebufferpool.ByteBuffer

// Write implements io.Writer - it appends p to ByteBuffer.B
func (b *ByteBuffer) Write(p []byte) (int, error) {
	return bb(b).Write(p)
}

// WriteString appends s to ByteBuffer.B
func (b *ByteBuffer) WriteString(s string) (int, error) {
	return bb(b).WriteString(s)
}

// Set sets ByteBuffer.B to p
func (b *ByteBuffer) Set(p []byte) {
	bb(b).Set(p)
}

// SetString sets ByteBuffer.B to s
func (b *ByteBuffer) SetString(s string) {
	bb(b).SetString(s)
}

// Reset makes ByteBuffer.B empty.
func (b *ByteBuffer) Reset() {
	bb(b).Reset()
}

// AcquireByteBuffer returns an empty byte buffer from the pool.
//
// Acquired byte buffer may be returned to the pool via ReleaseByteBuffer call.
// This reduces the number of memory allocations required for byte buffer
// management.
func AcquireByteBuffer() *ByteBuffer {
	return (*ByteBuffer)(defaultByteBufferPool.Get())
}

// ReleaseByteBuffer returns byte buffer to the pool.
//
// ByteBuffer.B mustn't be touched after returning it to the pool.
// Otherwise data races occur.
func ReleaseByteBuffer(b *ByteBuffer) {
	defaultByteBufferPool.Put(bb(b))
}

func bb(b *ByteBuffer) *bytebufferpool.ByteBuffer {
	return (*bytebufferpool.ByteBuffer)(b)
}

var defaultByteBufferPool bytebufferpool.Pool
