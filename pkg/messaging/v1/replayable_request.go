/*
Copyright 2021 The Dapr Authors
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

	streamutils "github.com/dapr/dapr/utils/streams"
)

// replayableRequest is implemented by InvokeMethodRequest and InvokeMethodResponse
type replayableRequest struct {
	data             io.ReadCloser
	replay           *bytes.Buffer
	lock             sync.Mutex
	currentTeeReader *streamutils.TeeReadCloser
}

// WithRawData sets message data.
func (rr *replayableRequest) WithRawData(data io.ReadCloser) {
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
		rr.replay = nil
	} else if rr.replay == nil {
		rr.replay = &bytes.Buffer{}
	}
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
		// If there's no data, and there's never been, just return a reader from nil
		r = bytes.NewReader(nil)
	} else if rr.replay != nil {
		// If there's replaying enabled
		rr.currentTeeReader = streamutils.NewTeeReadCloser(rr.data, rr.replay)
		r = io.MultiReader(
			bytes.NewReader(rr.replay.Bytes()),
			rr.currentTeeReader,
		)
	} else {
		// No replay enabled
		r = rr.data
	}

	return r
}

// Close the data stream.
func (rr *replayableRequest) Close() (err error) {
	rr.lock.Lock()
	defer rr.lock.Unlock()

	rr.replay = nil

	if rr.data != nil {
		err = rr.data.Close()
		if err != nil {
			return err
		}
		rr.data = nil
	}

	return nil
}
