/*
Copyright 2025 The Dapr Authors
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

package conversation

const (
	// SimulatedStreamingChunkSize is the default chunk size (bytes) for simulated streaming conversation data where
	// the entire response is sent in chunks to simulate a streaming response and allow clients to process data as it arrives
	// even if the model does not support streaming natively.
	SimulatedStreamingChunkSize = 1 << 12 // 4 KiB

	// PIIScrubberStreamingWindowSize is the default window size (bytes) for PII scrubbing while streaming conversation data.
	// this allows for a look-ahead window of stream data to be scrubbed
	PIIScrubberStreamingWindowSize = 128 // 128 bytes compromise between performance, memory usage and latency.
)
