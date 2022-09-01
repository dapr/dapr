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

	streamutils "github.com/dapr/dapr/utils/streams"
)

// replayableRequest is implemented by InvokeMethodRequest and InvokeMethodResponse
type replayableRequest struct {
	data   io.ReadCloser
	replay *bytes.Buffer
}

// SetReplay enables replaying for the data stream.
func (rr *replayableRequest) SetReplay(enabled bool) {
	if !enabled {
		rr.replay = nil
	} else if rr.replay == nil {
		if rr.data == nil {
			rr.data = io.NopCloser(bytes.NewReader(nil))
		}
		rr.replay = &bytes.Buffer{}
		rr.data = streamutils.TeeReadCloser(rr.data, rr.replay)
	}
}

// RawData returns the stream body.
// Note: this method is not safe for concurrent use.
func (rr *replayableRequest) RawData() (r io.Reader) {
	if rr.data == nil {
		r = bytes.NewReader(nil)
	} else if rr.replay != nil {
		r = io.MultiReader(
			bytes.NewReader(rr.replay.Bytes()),
			rr.data,
		)
	} else {
		r = rr.data
	}

	return r
}

// Close the data stream.
func (rr *replayableRequest) Close() (err error) {
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
