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
	"io"
	"strings"
	"testing"
)

func TestUppercaseTransformer(t *testing.T) {
	tests := []struct {
		name string
		in   string
		out  string
	}{
		{
			name: "empty string",
			in:   "",
			out:  "",
		},
		{
			name: "hello world",
			in:   "hello world",
			out:  "HELLO WORLD",
		},
		{
			name: "UPPERCASE",
			in:   "UPPERCASE",
			out:  "UPPERCASE",
		},
		{
			name: "Dante's verses (mixed case + unicode)",
			in:   "Nel mezzo del cammin di nostra vita mi ritrovai per una selva oscura, ché la diritta via era smarrita.",
			out:  "NEL MEZZO DEL CAMMIN DI NOSTRA VITA MI RITROVAI PER UNA SELVA OSCURA, CHÉ LA DIRITTA VIA ERA SMARRITA.",
		},
		{
			name: "emojis",
			in:   "😀ciao👩‍👩‍👦‍👦", // 😀 = single codepoint; 👩‍👩‍👦‍👦 = 7 (!!) codepoints
			out:  "😀CIAO👩‍👩‍👦‍👦",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr := UppercaseTransformer(strings.NewReader(tt.in))
			gotOut, err := io.ReadAll(tr)
			if err != nil {
				t.Errorf("read stream error = %v", err)
				return
			}
			if string(gotOut) != tt.out {
				t.Errorf("gotOut = %v, want %v", string(gotOut), tt.out)
			}
		})
	}
}
