package propagation

import (
	"github.com/valyala/fasthttp"
	"go.opentelemetry.io/otel/propagation"
)

var _ propagation.TextMapCarrier = &FasthttpSupplier{}

type FasthttpSupplier struct {
	header *fasthttp.RequestHeader
}

// NewFasthttpSupplier init FasthttpSupplier instance.
func NewFasthttpSupplier(header *fasthttp.RequestHeader) *FasthttpSupplier {
	return &FasthttpSupplier{
		header: header,
	}
}

// Get value from header key
func (f *FasthttpSupplier) Get(key string) string {
	return string(f.header.Peek(key))
}

// Set key-value to header
func (f *FasthttpSupplier) Set(key string, value string) {
	f.header.Set(key, value)
	return
}

// Keys get all keys from header
func (f *FasthttpSupplier) Keys() []string {
	var keys []string
	f.header.VisitAll(func(key, value []byte) {
		keys = append(keys, string(key))
	})
	return keys
}
