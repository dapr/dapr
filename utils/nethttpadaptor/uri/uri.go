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

package uri

import (
	"bytes"
	"path/filepath"
)

// NormalizePath is a function copied from
// https://github.com/valyala/fasthttp/blob/f0865d4aabbbea51a81d56ab31a3de2dfc5a9b05/uri.go#L575
// but with Path segment normalization removed. This is because the Dapr should
// leave encoded path segments _as is_, and not attempt to normalize them for
// forwarding them on to apps.
func NormalizePath(dst, src []byte) []byte {
	dst = dst[:0]
	dst = addLeadingSlash(dst, src)
	dst = append(dst, src...)

	// remove duplicate slashes
	b := dst
	bSize := len(b)
	for {
		n := bytes.Index(b, strSlashSlash)
		if n < 0 {
			break
		}
		b = b[n:]
		copy(b, b[1:])
		b = b[:len(b)-1]
		bSize--
	}
	dst = dst[:bSize]

	// remove /./ parts
	b = dst
	for {
		n := bytes.Index(b, strSlashDotSlash)
		if n < 0 {
			break
		}
		nn := n + len(strSlashDotSlash) - 1
		copy(b[n:], b[nn:])
		b = b[:len(b)-nn+n]
	}

	// remove /foo/../ parts
	for {
		n := bytes.Index(b, strSlashDotDotSlash)
		if n < 0 {
			break
		}
		nn := bytes.LastIndexByte(b[:n], '/')
		if nn < 0 {
			nn = 0
		}
		n += len(strSlashDotDotSlash) - 1
		copy(b[nn:], b[n:])
		b = b[:len(b)-n+nn]
	}

	// remove trailing /foo/..
	n := bytes.LastIndex(b, strSlashDotDot)
	if n >= 0 && n+len(strSlashDotDot) == len(b) {
		nn := bytes.LastIndexByte(b[:n], '/')
		if nn < 0 {
			return append(dst[:0], strSlash...)
		}
		b = b[:nn+1]
	}

	if filepath.Separator == '\\' {
		// remove \.\ parts
		b = dst
		for {
			n := bytes.Index(b, strBackSlashDotBackSlash)
			if n < 0 {
				break
			}
			nn := n + len(strSlashDotSlash) - 1
			copy(b[n:], b[nn:])
			b = b[:len(b)-nn+n]
		}

		// remove /foo/..\ parts
		for {
			n := bytes.Index(b, strSlashDotDotBackSlash)
			if n < 0 {
				break
			}
			nn := bytes.LastIndexByte(b[:n], '/')
			if nn < 0 {
				nn = 0
			}
			n += len(strSlashDotDotBackSlash) - 1
			copy(b[nn:], b[n:])
			b = b[:len(b)-n+nn]
		}

		// remove /foo\..\ parts
		for {
			n := bytes.Index(b, strBackSlashDotDotBackSlash)
			if n < 0 {
				break
			}
			nn := bytes.LastIndexByte(b[:n], '/')
			if nn < 0 {
				nn = 0
			}
			n += len(strBackSlashDotDotBackSlash) - 1
			copy(b[nn:], b[n:])
			b = b[:len(b)-n+nn]
		}

		// remove trailing \foo\..
		n := bytes.LastIndex(b, strBackSlashDotDot)
		if n >= 0 && n+len(strSlashDotDot) == len(b) {
			nn := bytes.LastIndexByte(b[:n], '/')
			if nn < 0 {
				return append(dst[:0], strSlash...)
			}
			b = b[:nn+1]
		}
	}

	return b
}
