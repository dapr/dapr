package documentdb

import (
	"bytes"
	"encoding/json"
	"io"
)

// JSONEncoder describes json encoder
type JSONEncoder interface {
	Encode(val interface{}) error
}

// JSONDecoder describes json decoder
type JSONDecoder interface {
	Decode(obj interface{}) error
}

// Marshal function type
type Marshal func(v interface{}) ([]byte, error)

// Unmarshal function type
type Unmarshal func(data []byte, v interface{}) error

// EncoderFactory describes function that creates json encoder
type EncoderFactory func(*bytes.Buffer) JSONEncoder

// DecoderFactory describes function that creates json decoder
type DecoderFactory func(io.Reader) JSONDecoder

// SerializationDriver struct holds serialization / deserilization providers
type SerializationDriver struct {
	EncoderFactory EncoderFactory
	DecoderFactory DecoderFactory
	Marshal        Marshal
	Unmarshal      Unmarshal
}

// DefaultSerialization holds default stdlib json driver
var DefaultSerialization = SerializationDriver{
	EncoderFactory: func(b *bytes.Buffer) JSONEncoder {
		return json.NewEncoder(b)
	},
	DecoderFactory: func(r io.Reader) JSONDecoder {
		return json.NewDecoder(r)
	},
	Marshal:   json.Marshal,
	Unmarshal: json.Unmarshal,
}

// Serialization holds driver that is actually used
var Serialization = DefaultSerialization
