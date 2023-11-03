// This file contains stream utils that are only used in tests
//go:build unit
// +build unit

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

package testing

import (
	"io"
)

// ErrorReader implements an io.Reader that returns an error after reading a few bytes.
type ErrorReader struct{}

func (r ErrorReader) Read(p []byte) (n int, err error) {
	data := []byte("non ti scordar di me")
	l := copy(p, data)
	return l, io.ErrClosedPipe
}
