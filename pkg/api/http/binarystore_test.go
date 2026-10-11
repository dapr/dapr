//go:build unit

/*
Copyright 2026 The Dapr Authors
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

package http

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestStreamBinaryFileAbortsTruncatedResponse(t *testing.T) {
	streamHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		streamBinaryFile(
			w,
			io.MultiReader(strings.NewReader("partial"), errorReader{}),
			"store",
			"file",
		)
	})
	recoveringHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			_ = recover()
		}()
		streamHandler.ServeHTTP(w, r)
	})

	server := httptest.NewServer(recoveringHandler)
	t.Cleanup(server.Close)

	resp, err := server.Client().Get(server.URL)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, resp.Body.Close()) })

	body, err := io.ReadAll(resp.Body)
	require.ErrorIs(t, err, io.ErrUnexpectedEOF)
	require.Equal(t, []byte("partial"), body)
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) {
	return 0, errors.New("read failed")
}

func TestStreamBinaryFileAbortsOnShortWrite(t *testing.T) {
	w := &shortWriteResponseWriter{
		header: make(http.Header),
	}

	streamBinaryFile(w, strings.NewReader("payload"), "store", "file")

	require.True(t, w.deadlineSet)
}

type shortWriteResponseWriter struct {
	header      http.Header
	deadlineSet bool
}

func (s *shortWriteResponseWriter) Header() http.Header {
	return s.header
}

func (*shortWriteResponseWriter) WriteHeader(int) {}

func (*shortWriteResponseWriter) Write(p []byte) (int, error) {
	return len(p) - 1, nil
}

func (*shortWriteResponseWriter) Flush() {}

func (s *shortWriteResponseWriter) SetWriteDeadline(time.Time) error {
	s.deadlineSet = true
	return nil
}
