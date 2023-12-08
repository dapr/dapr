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

package responsewriter

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResponseWriterBeforeWrite(t *testing.T) {
	rec := httptest.NewRecorder()
	rw := NewResponseWriter(rec)

	require.Equal(t, 0, rw.Status())
	require.False(t, rw.Written())
}

func TestResponseWriterBeforeFuncHasAccessToStatus(t *testing.T) {
	var status int

	rec := httptest.NewRecorder()
	rw := NewResponseWriter(rec)

	rw.Before(func(w ResponseWriter) {
		status = w.Status()
	})
	rw.WriteHeader(http.StatusCreated)

	require.Equal(t, http.StatusCreated, status)
}

func TestResponseWriterBeforeFuncCanChangeStatus(t *testing.T) {
	rec := httptest.NewRecorder()
	rw := NewResponseWriter(rec)

	// Always respond with 200.
	rw.Before(func(w ResponseWriter) {
		w.WriteHeader(http.StatusOK)
	})

	rw.WriteHeader(http.StatusBadRequest)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestResponseWriterBeforeFuncChangesStatusMultipleTimes(t *testing.T) {
	rec := httptest.NewRecorder()
	rw := NewResponseWriter(rec)

	rw.Before(func(w ResponseWriter) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	rw.Before(func(w ResponseWriter) {
		w.WriteHeader(http.StatusNotFound)
	})

	rw.WriteHeader(http.StatusOK)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestResponseWriterWritingString(t *testing.T) {
	rec := httptest.NewRecorder()
	rw := NewResponseWriter(rec)

	rw.Write([]byte("Hello world"))

	require.Equal(t, rec.Code, rw.Status())
	require.Equal(t, "Hello world", rec.Body.String())
	require.Equal(t, http.StatusOK, rw.Status())
	require.Equal(t, 11, rw.Size())
	require.True(t, rw.Written())
}

func TestResponseWriterWritingStrings(t *testing.T) {
	rec := httptest.NewRecorder()
	rw := NewResponseWriter(rec)

	rw.Write([]byte("Hello world"))
	rw.Write([]byte("foo bar bat baz"))

	require.Equal(t, rec.Code, rw.Status())
	require.Equal(t, "Hello worldfoo bar bat baz", rec.Body.String())
	require.Equal(t, http.StatusOK, rw.Status())
	require.Equal(t, 26, rw.Size())
}

func TestResponseWriterWritingHeader(t *testing.T) {
	rec := httptest.NewRecorder()
	rw := NewResponseWriter(rec)

	rw.WriteHeader(http.StatusNotFound)

	require.Equal(t, rec.Code, rw.Status())
	require.Equal(t, "", rec.Body.String())
	require.Equal(t, http.StatusNotFound, rw.Status())
	require.Equal(t, 0, rw.Size())
}

func TestResponseWriterWritingHeaderTwice(t *testing.T) {
	rec := httptest.NewRecorder()
	rw := NewResponseWriter(rec)

	rw.WriteHeader(http.StatusNotFound)
	rw.WriteHeader(http.StatusInternalServerError)

	require.Equal(t, rw.Status(), rec.Code)
	require.Equal(t, "", rec.Body.String())
	require.Equal(t, http.StatusNotFound, rw.Status())
	require.Equal(t, 0, rw.Size())
}

func TestResponseWriterBefore(t *testing.T) {
	rec := httptest.NewRecorder()
	rw := NewResponseWriter(rec)
	result := ""

	rw.Before(func(ResponseWriter) {
		result += "foo"
	})
	rw.Before(func(ResponseWriter) {
		result += "bar"
	})

	rw.WriteHeader(http.StatusNotFound)

	require.Equal(t, rec.Code, rw.Status())
	require.Equal(t, "", rec.Body.String())
	require.Equal(t, http.StatusNotFound, rw.Status())
	require.Equal(t, 0, rw.Size())
	require.Equal(t, "barfoo", result)
}

func TestResponseWriterUnwrap(t *testing.T) {
	rec := httptest.NewRecorder()
	rw := NewResponseWriter(rec)
	switch v := rw.(type) {
	case interface{ Unwrap() http.ResponseWriter }:
		require.Equal(t, v.Unwrap(), rec)
	default:
		t.Error("Does not implement Unwrap()")
	}
}

// mockReader only implements io.Reader without other methods like WriterTo
type mockReader struct {
	readStr string
	eof     bool
}

func (r *mockReader) Read(p []byte) (n int, err error) {
	if r.eof {
		return 0, io.EOF
	}
	copy(p, r.readStr)
	r.eof = true
	return len(r.readStr), nil
}

func TestResponseWriterWithoutReadFrom(t *testing.T) {
	writeString := "Hello world"

	rec := httptest.NewRecorder()
	rw := NewResponseWriter(rec)

	n, err := io.Copy(rw, &mockReader{readStr: writeString})
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, rw.Status())
	require.True(t, rw.Written())
	require.Len(t, writeString, rw.Size())
	require.Len(t, writeString, int(n))
	require.Equal(t, writeString, rec.Body.String())
}

type mockResponseWriterWithReadFrom struct {
	*httptest.ResponseRecorder
	writtenStr string
}

func (rw *mockResponseWriterWithReadFrom) ReadFrom(r io.Reader) (n int64, err error) {
	bytes, err := io.ReadAll(r)
	if err != nil {
		return 0, err
	}
	rw.writtenStr = string(bytes)
	rw.ResponseRecorder.Write(bytes)
	return int64(len(bytes)), nil
}

func TestResponseWriterWithReadFrom(t *testing.T) {
	writeString := "Hello world"
	mrw := &mockResponseWriterWithReadFrom{ResponseRecorder: httptest.NewRecorder()}
	rw := NewResponseWriter(mrw)
	n, err := io.Copy(rw, &mockReader{readStr: writeString})
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, rw.Status())
	require.True(t, rw.Written())
	require.Len(t, writeString, rw.Size())
	require.Len(t, writeString, int(n))
	require.Equal(t, writeString, mrw.Body.String())
	require.Equal(t, writeString, mrw.writtenStr)
}
