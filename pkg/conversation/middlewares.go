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

import (
	"fmt"
	"unicode"

	piiscrubber "github.com/aavaz-ai/pii-scrubber"
)

/*
STREAMING PII SCRUBBER IMPLEMENTATION

This implementation combines window-based performance with token-aware boundaries
to provide robust PII scrubbing for streaming conversation data.

## APPROACH

The StreamingPIIScrubber uses a hybrid approach that:
1. Uses a fixed window size for predictable memory usage and performance
2. Respects word boundaries to avoid splitting PII across processing chunks
3. Processes content when the window is full, but only up to the last complete word
4. Keeps incomplete words in the buffer for the next processing cycle

## SCRUBBER LIBRARY BUG DISCOVERED

During testing, we discovered a significant bug in the github.com/aavaz-ai/pii-scrubber library:

**BUG**: When IP addresses and credit card numbers appear in the same text, the library
fails to detect credit card numbers. This affects ALL usage of the library, not just streaming.

**EVIDENCE**:
- ✅ "Card number 4263982640269299" → "Card number <CREDIT_CARD>" (WORKS)
- ❌ "IP is 192.168.1.100 and card 4263982640269299" → "IP is <IP> and card 4263982640269299" (FAILS)

**WORKAROUNDS IMPLEMENTED**:
- Conditional dual-pass processing: Automatically runs a second scrubbing pass only when the first
  pass detects PII, providing both performance optimization and bug mitigation.
*/

// StreamingPIIScrubber combines window-based performance with token-aware boundaries
// It uses a window size for performance but ensures we don't split words at the boundary
type StreamingPIIScrubber struct {
	buffer     []byte
	windowSize int
	scrubber   piiscrubber.Scrubber
}

// NewStreamingPIIScrubber creates a streaming PII scrubber with the default window size
func NewStreamingPIIScrubber() (*StreamingPIIScrubber, error) {
	return NewStreamingPIIScrubberWithWindowSize(PIIScrubberStreamingWindowSize)
}

// NewStreamingPIIScrubberWithWindowSize creates a streaming PII scrubber with a custom window size
func NewStreamingPIIScrubberWithWindowSize(windowSize int) (*StreamingPIIScrubber, error) {
	scrubber := &StreamingPIIScrubber{
		buffer:     make([]byte, 0, windowSize*2), // 2x window size to allow for incomplete words. Reconsider using sync.Pool if this is used in a long-running/high-throughput application.
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

// isTokenDelimiter checks if a character is a token delimiter using Unicode categories
func isTokenDelimiter(r rune) bool {
	return unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r)
}

// findLastCompleteWordPosition finds the position after the last complete word
// Returns -1 if no complete word boundary found
func findLastCompleteWordPosition(data []byte) int {
	if len(data) == 0 {
		return -1
	}

	// Find the last separator that creates a complete word boundary
	for i := len(data) - 1; i >= 0; i-- {
		if isTokenDelimiter(rune(data[i])) {
			// Check if there's non-delimiter content after this position
			hasContentAfter := false
			for j := i + 1; j < len(data); j++ {
				if !isTokenDelimiter(rune(data[j])) {
					hasContentAfter = true
					break
				}
			}

			// If no content after delimiter, it's a good word boundary
			if !hasContentAfter {
				return i + 1
			}
		}
	}

	return -1
}

// ProcessChunk processes chunks using window size but respects word boundaries
func (s *StreamingPIIScrubber) ProcessChunk(chunk []byte) ([]byte, error) {
	if len(chunk) == 0 {
		return chunk, nil
	}

	s.buffer = append(s.buffer, chunk...)

	// Buffer until window size is reached
	if len(s.buffer) <= s.windowSize {
		return nil, nil
	}

	// Find the last complete word within the window
	windowContent := s.buffer[:s.windowSize]
	lastCompletePos := findLastCompleteWordPosition(windowContent)

	if lastCompletePos == -1 {
		// No complete words - prevent infinite buffering
		if len(s.buffer) > s.windowSize*2 {
			return s.processAndTrim(s.windowSize)
		}
		return nil, nil
	}

	return s.processAndTrim(lastCompletePos)
}

// processAndTrim processes the first n bytes and removes them from buffer
func (s *StreamingPIIScrubber) processAndTrim(n int) ([]byte, error) {
	if n <= 0 || n > len(s.buffer) {
		return nil, nil
	}

	processContent := s.buffer[:n]
	scrubbed, err := s.scrubPII(processContent)
	if err != nil {
		return nil, err
	}

	// Remove processed content from buffer
	remaining := s.buffer[n:]
	s.buffer = make([]byte, len(remaining))
	copy(s.buffer, remaining)

	return scrubbed, nil
}

// Flush processes any remaining content in the buffer
func (s *StreamingPIIScrubber) Flush() ([]byte, error) {
	if len(s.buffer) == 0 {
		return nil, nil
	}

	scrubbed, err := s.scrubPII(s.buffer)
	if err != nil {
		return nil, err
	}

	s.buffer = s.buffer[:0] // Clear buffer to allow re-use (GC will collect it if not re-used)
	return scrubbed, nil
}

// scrubPII applies conditional dual-pass PII scrubbing to handle library bug
// Only runs a second pass if the first pass detected PII, optimizing performance
func (s *StreamingPIIScrubber) scrubPII(content []byte) ([]byte, error) {
	if s.scrubber == nil {
		return content, nil
	}

	original := string(content)

	// First pass
	firstPass, err := s.scrubber.ScrubTexts([]string{original})
	if err != nil {
		return content, err
	}

	if len(firstPass) == 0 {
		return content, nil
	}

	result := firstPass[0]

	// Only run second pass if first pass detected PII (changed something)
	if result != original {
		// Run second pass to catch library bugs (e.g., IP+credit card interference)
		secondPass, err := s.scrubber.ScrubTexts([]string{result})
		if err == nil && len(secondPass) > 0 {
			result = secondPass[0]
		}
	}

	return []byte(result), nil
}
