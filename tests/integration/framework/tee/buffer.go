/*
Copyright 2024 The Dapr Authors
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

package tee

import (
	"bytes"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// buffer tees a Reader to multiple buffered Readers.
type buffer struct {
	cond *sync.Cond
	data []byte
	bufs []*bufReader
	err  error
}

type bufReader struct {
	buffer *buffer
	data   *bytes.Buffer
}

// Buffer creates a buffered `tee` for multiple readers. Input reader must be
// EOF terminated before end of test.
func Buffer(t *testing.T, reader io.Reader) Reader {
	b := &buffer{
		cond: sync.NewCond(&sync.Mutex{}),
	}
	closed := make(chan struct{})

	go func() {
		for {
			d := make([]byte, 512)
			n, err := reader.Read(d)
			b.cond.L.Lock()
			b.data = append(b.data, d[:n]...)
			b.err = err

			for _, buf := range b.bufs {
				var nBuf int
				for {
					written, werr := buf.data.Write(d[:n])
					if werr != nil {
						break
					}
					nBuf += written
					if nBuf == n {
						break
					}
				}
			}

			b.cond.L.Unlock()
			b.cond.Broadcast()

			if err != nil {
				close(closed)
				return
			}
		}
	}()

	t.Cleanup(func() {
		select {
		case <-closed:
		case <-time.After(5 * time.Second):
			require.FailNow(t, "timeout waiting for buffered to close")
		}
	})

	return b
}

// Add adds a new reader to the buffered.
// Readers can be added at any time, even after the input reader has been
// closed.
// Added readers will always read the full content of the input reader.
func (b *buffer) Add(t *testing.T) io.Reader {
	b.cond.L.Lock()
	defer b.cond.L.Unlock()

	var data bytes.Buffer
	var n int
	for {
		written, err := data.Write(b.data)
		require.NoError(t, err)
		n += written
		if n == len(b.data) {
			break
		}
	}

	bufReader := &bufReader{
		buffer: b,
		data:   &data,
	}
	b.bufs = append(b.bufs, bufReader)

	return bufReader
}

// Read implements the io.Reader interface.
func (b *bufReader) Read(data []byte) (int, error) {
	b.buffer.cond.L.Lock()
	defer b.buffer.cond.L.Unlock()

	for {
		n, err := b.data.Read(data)

		// If not closed and no read, wait for writing.
		ok := err == io.EOF && b.buffer.err == nil && n == 0
		if ok {
			b.buffer.cond.Wait()
			continue
		}

		if err == io.EOF {
			return n, b.buffer.err
		}

		return n, err
	}
}
