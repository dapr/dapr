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

package universal

import (
	"fmt"
	"regexp"

	piiscrubber "github.com/aavaz-ai/pii-scrubber"
)

// StreamingPIIScrubber handles real-time PII scrubbing for streaming content
type StreamingPIIScrubber struct {
	buffer     []byte
	windowSize int // Look-ahead window for PII detection
	scrubber   piiscrubber.Scrubber
}

// PIIPattern represents a PII detection pattern
type PIIPattern struct {
	Pattern     *regexp.Regexp
	Replacement string
}

// NewStreamingPIIScrubber creates a new streaming PII scrubber with a look-ahead window
func NewStreamingPIIScrubber(windowSize int) (*StreamingPIIScrubber, error) {
	scrubber := &StreamingPIIScrubber{
		buffer:     make([]byte, 0, windowSize*2),
		windowSize: windowSize,
	}

	// Initialize the default PII scrubber
	defaultScrubber, err := piiscrubber.NewDefaultScrubber()
	if err != nil {
		return nil, fmt.Errorf("failed to create default PII scrubber: %w", err)
	}
	scrubber.scrubber = defaultScrubber

	return scrubber, nil
}

// ProcessChunk processes a chunk of streaming content with PII scrubbing
func (s *StreamingPIIScrubber) ProcessChunk(chunk []byte) []byte {
	if len(chunk) == 0 {
		return chunk
	}

	// Append to buffer
	s.buffer = append(s.buffer, chunk...)

	// Process what we can safely process (leaving window for PII detection)
	safeLength := len(s.buffer) - s.windowSize
	if safeLength <= 0 {
		return nil // Need more data
	}

	// Extract safe portion
	safeContent := s.buffer[:safeLength]

	// Apply PII scrubbing to safe portion
	scrubbed := s.scrubPII(safeContent)

	// Keep remaining buffer for next iteration
	s.buffer = s.buffer[safeLength:]

	return scrubbed
}

// Flush processes and returns any remaining content in the buffer
func (s *StreamingPIIScrubber) Flush() []byte {
	if len(s.buffer) == 0 {
		return nil
	}

	// Process remaining buffer content
	scrubbed := s.scrubPII(s.buffer)
	s.buffer = s.buffer[:0] // Clear buffer

	return scrubbed
}

// scrubPII applies PII scrubbing patterns to the content
func (s *StreamingPIIScrubber) scrubPII(content []byte) []byte {
	if s.scrubber == nil {
		return content
	}

	// Convert to string for processing
	text := string(content)

	// Use the default scrubber to process the text
	scrubbed, err := s.scrubber.ScrubTexts([]string{text})
	if err != nil {
		// If scrubbing fails, return original content
		// TODO: Add logging here
		return content
	}

	if len(scrubbed) > 0 {
		return []byte(scrubbed[0])
	}

	return content
}
