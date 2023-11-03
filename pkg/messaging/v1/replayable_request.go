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

package v1

import (
	"bytes"
	"io"
	"sync"

	"github.com/dapr/kit/byteslicepool"
	streamutils "github.com/dapr/kit/streams"
)

// Minimum capacity for the slices is 2KB
const minByteSliceCapacity = 2 << 10

// Contain pools of *bytes.Buffer and []byte objects.
// Used to reduce the number of allocations in replayableRequest for buffers and relieve pressure on the GC.
var (
	bufPool = sync.Pool{New: newBuffer}
	bsPool  = byteslicepool.NewByteSlicePool(minByteSliceCapacity)
)

func newBuffer() any {
	return new(bytes.Buffer)
}

// replayableRequest is implemented by InvokeMethodRequest and InvokeMethodResponse
type replayableRequest struct {
	data             io.Reader
	replay           *bytes.Buffer
	lock             sync.Mutex
	currentTeeReader *streamutils.TeeReadCloser
	currentData      []byte
}

// WithRawData sets message data.
func (rr *replayableRequest) WithRawData(data io.Reader) {
	rr.lock.Lock()
	defer rr.lock.Unlock()

	if rr.replay != nil {
		// We are panicking here because we can't return errors
		// This is just to catch issues during development however, and will never happen at runtime
		panic("WithRawData cannot be invoked after replaying has been enabled")
	}

	rr.data = data
}

// SetReplay enables replaying for the data stream.
func (rr *replayableRequest) SetReplay(enabled bool) {
	rr.lock.Lock()
	defer rr.lock.Unlock()

	if !enabled {
		rr.closeReplay()
	} else if rr.replay == nil {
		rr.replay = bufPool.Get().(*bytes.Buffer)
		rr.replay.Reset()
	}
}

// CanReplay returns true if the data stream can be replayed.
func (rr *replayableRequest) CanReplay() bool {
	return rr.replay != nil
}

// RawData returns the stream body.
func (rr *replayableRequest) RawData() (r io.Reader) {
	rr.lock.Lock()
	defer rr.lock.Unlock()

	// If there's a previous TeeReadCloser, stop it so readers won't add more data into its replay buffer
	if rr.currentTeeReader != nil {
		_ = rr.currentTeeReader.Stop()
	}

	if rr.data == nil {
		// If there's no data, and there's never been, just return a reader with no data
		r = bytes.NewReader(nil)
	} else if rr.replay != nil {
		// If there's replaying enabled, we need to create a new TeeReadCloser
		// We need to copy the data read insofar from the reply buffer because the buffer becomes invalid after new data is written into the it, then reset the buffer
		l := rr.replay.Len()

		// Get a new byte slice from the pool if we don't have one, and ensure it has enough capacity
		if rr.currentData == nil {
			rr.currentData = bsPool.Get(l)
		}
		rr.currentData = bsPool.Resize(rr.currentData, l)

		// Copy the data from the replay buffer into the byte slice
		copy(rr.currentData[0:l], rr.replay.Bytes())
		rr.replay.Reset()

		// Create a new TeeReadCloser that reads from the previously-read data and then the data not yet processed
		// The TeeReadCloser also keeps all the data it reads into the replay buffer
		mr := streamutils.NewMultiReaderCloser(
			bytes.NewReader(rr.currentData[0:l]),
			rr.data,
		)
		rr.currentTeeReader = streamutils.NewTeeReadCloser(mr, rr.replay)
		r = rr.currentTeeReader
	} else {
		// No replay enabled
		r = rr.data
	}

	return r
}

func (rr *replayableRequest) closeReplay() {
	// Return the buffer and byte slice to the pools if we got one
	if rr.replay != nil {
		bufPool.Put(rr.replay)
		rr.replay = nil
	}
	if rr.currentData != nil {
		bsPool.Put(rr.currentData)
		rr.currentData = nil
	}
}

// Close the data stream and replay buffers.
// It's safe to call Close multiple times on the same object.
func (rr *replayableRequest) Close() (err error) {
	rr.lock.Lock()
	defer rr.lock.Unlock()

	rr.closeReplay()

	if rr.data != nil {
		if rc, ok := rr.data.(io.Closer); ok {
			err = rc.Close()
			if err != nil {
				return err
			}
		}
		rr.data = nil
	}

	return nil
}
