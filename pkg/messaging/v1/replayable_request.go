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

	streamutils "github.com/dapr/kit/streams"
)

// Minimum capacity for the slices is 2KB
const minByteSliceCapacity = 2 << 10

// Contains a pool of *bytes.Buffer objects.
// Used to reduce the number of allocations in replayableRequest for buffers and relieve pressure on the GC.
var bufPool = sync.Pool{New: newBuffer}

func newBuffer() any {
	return new(bytes.Buffer)
}

// replayableRequest is implemented by InvokeMethodRequest and InvokeMethodResponse
type replayableRequest struct {
	data             io.Reader
	replay           *bytes.Buffer
	lock             sync.Mutex
	currentTeeReader *streamutils.TeeReadCloser
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
		// Replay is enabled: everything read so far stays in the replay buffer (it is never reset),
		// so a RawData() call that is never read from cannot lose data.
		// New readers get a snapshot of the bytes read so far, followed by the rest of the stream,
		// which is teed into the replay buffer.
		l := rr.replay.Len()
		snapshot := rr.replay.Bytes()[:l:l]
		rr.currentTeeReader = streamutils.NewTeeReadCloser(rr.data, rr.replay)
		r = streamutils.NewMultiReaderCloser(bytes.NewReader(snapshot), rr.currentTeeReader)
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
