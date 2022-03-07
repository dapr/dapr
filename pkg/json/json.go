/*
Copyright 2022 The Dapr Authors
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

package json

import (
	ejson "encoding/json"
	"io"
)

// RawMessage is a wrapper around encoding/json's RawMessage
// that is a raw encoded JSON value.
type RawMessage ejson.RawMessage

// Marshal is a wrapper around encoding/json's Marshal
// that returns the JSON encoding of v.
func Marshal(v interface{}) ([]byte, error) {
	return ejson.Marshal(v)
}

// Unmarshal is a wrapper around encoding/json's Unmarshal
// that parses the JSON-encoded data and stores the result
// in the value pointed to by v.
func Unmarshal(data []byte, v interface{}) error {
	return ejson.Unmarshal(data, v)
}

// NewDecoder is a wrapper around encoding/json's NewDecoder
// that returns a new decoder that reads from r.
func NewDecoder(r io.Reader) *ejson.Decoder {
	return ejson.NewDecoder(r)
}

// NewEncoder is a wrapper around encoding/json's NewEncoder
// that returns a new encoder that writes to w.
func NewEncoder(w io.Writer) *ejson.Encoder {
	return ejson.NewEncoder(w)
}
