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

package streams

import (
	"bufio"
	"io"
	"strings"
	"unicode"

	"github.com/tidwall/transform"
)

// UppercaseTransformer uppercases all characters in the stream, by calling strings.ToUpper() on them.
func UppercaseTransformer(r io.Reader) io.Reader {
	br := bufio.NewReader(r)
	return transform.NewTransformer(func() ([]byte, error) {
		c, _, err := br.ReadRune()
		if err != nil {
			return nil, err
		}

		return RuneToUppercase(c), nil
	})
}

// RuneToUppercase converts a rune into a byte slice where all lowercase letters (Unicode-aware) are converted to uppercase ones.
func RuneToUppercase(c rune) []byte {
	// Optimize for ASCII characters
	if c < 128 {
		b := byte(c)
		if 'a' <= b && b <= 'z' {
			return []byte{b - 0x20}
		} else {
			return []byte{b}
		}
	}

	// Unicode
	return []byte(strings.Map(unicode.ToUpper, string([]rune{c})))
}
