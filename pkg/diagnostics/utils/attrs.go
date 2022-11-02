package utils

import (
	"go.opentelemetry.io/otel/attribute"
)

type Attrs []attribute.KeyValue

// Get search key and return value
func (t Attrs) Get(key attribute.Key) string {
	if t == nil {
		return ""
	}
	for _, attr := range t {
		if attr.Key == key {
			return attr.Value.AsString()
		}
	}
	return ""
}
