package fasthttp

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"reflect"
	"strings"
	"sync"
	"time"
	"unsafe"
)

// AppendHTMLEscape appends html-escaped s to dst and returns the extended dst.
func AppendHTMLEscape(dst []byte, s string) []byte {
	if strings.IndexByte(s, '<') < 0 &&
		strings.IndexByte(s, '>') < 0 &&
		strings.IndexByte(s, '"') < 0 &&
		strings.IndexByte(s, '\'') < 0 {

		// fast path - nothing to escape
		return append(dst, s...)
	}

	// slow path
	var prev int
	var sub string
	for i, n := 0, len(s); i < n; i++ {
		sub = ""
		switch s[i] {
		case '<':
			sub = "&lt;"
		case '>':
			sub = "&gt;"
		case '"':
			sub = "&quot;"
		case '\'':
			sub = "&#39;"
		}
		if len(sub) > 0 {
			dst = append(dst, s[prev:i]...)
			dst = append(dst, sub...)
			prev = i + 1
		}
	}
	return append(dst, s[prev:]...)
}

// AppendHTMLEscapeBytes appends html-escaped s to dst and returns
// the extended dst.
func AppendHTMLEscapeBytes(dst, s []byte) []byte {
	return AppendHTMLEscape(dst, b2s(s))
}

// AppendIPv4 appends string representation of the given ip v4 to dst
// and returns the extended dst.
func AppendIPv4(dst []byte, ip net.IP) []byte {
	ip = ip.To4()
	if ip == nil {
		return append(dst, "non-v4 ip passed to AppendIPv4"...)
	}

	dst = AppendUint(dst, int(ip[0]))
	for i := 1; i < 4; i++ {
		dst = append(dst, '.')
		dst = AppendUint(dst, int(ip[i]))
	}
	return dst
}

var errEmptyIPStr = errors.New("empty ip address string")

// ParseIPv4 parses ip address from ipStr into dst and returns the extended dst.
func ParseIPv4(dst net.IP, ipStr []byte) (net.IP, error) {
	if len(ipStr) == 0 {
		return dst, errEmptyIPStr
	}
	if len(dst) < net.IPv4len {
		dst = make([]byte, net.IPv4len)
	}
	copy(dst, net.IPv4zero)
	dst = dst.To4()
	if dst == nil {
		panic("BUG: dst must not be nil")
	}

	b := ipStr
	for i := 0; i < 3; i++ {
		n := bytes.IndexByte(b, '.')
		if n < 0 {
			return dst, fmt.Errorf("cannot find dot in ipStr %q", ipStr)
		}
		v, err := ParseUint(b[:n])
		if err != nil {
			return dst, fmt.Errorf("cannot parse ipStr %q: %s", ipStr, err)
		}
		if v > 255 {
			return dst, fmt.Errorf("cannot parse ipStr %q: ip part cannot exceed 255: parsed %d", ipStr, v)
		}
		dst[i] = byte(v)
		b = b[n+1:]
	}
	v, err := ParseUint(b)
	if err != nil {
		return dst, fmt.Errorf("cannot parse ipStr %q: %s", ipStr, err)
	}
	if v > 255 {
		return dst, fmt.Errorf("cannot parse ipStr %q: ip part cannot exceed 255: parsed %d", ipStr, v)
	}
	dst[3] = byte(v)

	return dst, nil
}

// AppendHTTPDate appends HTTP-compliant (RFC1123) representation of date
// to dst and returns the extended dst.
func AppendHTTPDate(dst []byte, date time.Time) []byte {
	dst = date.In(time.UTC).AppendFormat(dst, time.RFC1123)
	copy(dst[len(dst)-3:], strGMT)
	return dst
}

// ParseHTTPDate parses HTTP-compliant (RFC1123) date.
func ParseHTTPDate(date []byte) (time.Time, error) {
	return time.Parse(time.RFC1123, b2s(date))
}

// AppendUint appends n to dst and returns the extended dst.
func AppendUint(dst []byte, n int) []byte {
	if n < 0 {
		panic("BUG: int must be positive")
	}

	var b [20]byte
	buf := b[:]
	i := len(buf)
	var q int
	for n >= 10 {
		i--
		q = n / 10
		buf[i] = '0' + byte(n-q*10)
		n = q
	}
	i--
	buf[i] = '0' + byte(n)

	dst = append(dst, buf[i:]...)
	return dst
}

// ParseUint parses uint from buf.
func ParseUint(buf []byte) (int, error) {
	v, n, err := parseUintBuf(buf)
	if n != len(buf) {
		return -1, errUnexpectedTrailingChar
	}
	return v, err
}

var (
	errEmptyInt               = errors.New("empty integer")
	errUnexpectedFirstChar    = errors.New("unexpected first char found. Expecting 0-9")
	errUnexpectedTrailingChar = errors.New("unexpected traling char found. Expecting 0-9")
	errTooLongInt             = errors.New("too long int")
)

func parseUintBuf(b []byte) (int, int, error) {
	n := len(b)
	if n == 0 {
		return -1, 0, errEmptyInt
	}
	v := 0
	for i := 0; i < n; i++ {
		c := b[i]
		k := c - '0'
		if k > 9 {
			if i == 0 {
				return -1, i, errUnexpectedFirstChar
			}
			return v, i, nil
		}
		if i >= maxIntChars {
			return -1, i, errTooLongInt
		}
		v = 10*v + int(k)
	}
	return v, n, nil
}

var (
	errEmptyFloat           = errors.New("empty float number")
	errDuplicateFloatPoint  = errors.New("duplicate point found in float number")
	errUnexpectedFloatEnd   = errors.New("unexpected end of float number")
	errInvalidFloatExponent = errors.New("invalid float number exponent")
	errUnexpectedFloatChar  = errors.New("unexpected char found in float number")
)

// ParseUfloat parses unsigned float from buf.
func ParseUfloat(buf []byte) (float64, error) {
	if len(buf) == 0 {
		return -1, errEmptyFloat
	}
	b := buf
	var v uint64
	var offset = 1.0
	var pointFound bool
	for i, c := range b {
		if c < '0' || c > '9' {
			if c == '.' {
				if pointFound {
					return -1, errDuplicateFloatPoint
				}
				pointFound = true
				continue
			}
			if c == 'e' || c == 'E' {
				if i+1 >= len(b) {
					return -1, errUnexpectedFloatEnd
				}
				b = b[i+1:]
				minus := -1
				switch b[0] {
				case '+':
					b = b[1:]
					minus = 1
				case '-':
					b = b[1:]
				default:
					minus = 1
				}
				vv, err := ParseUint(b)
				if err != nil {
					return -1, errInvalidFloatExponent
				}
				return float64(v) * offset * math.Pow10(minus*int(vv)), nil
			}
			return -1, errUnexpectedFloatChar
		}
		v = 10*v + uint64(c-'0')
		if pointFound {
			offset /= 10
		}
	}
	return float64(v) * offset, nil
}

var (
	errEmptyHexNum    = errors.New("empty hex number")
	errTooLargeHexNum = errors.New("too large hex number")
)

func readHexInt(r *bufio.Reader) (int, error) {
	n := 0
	i := 0
	var k int
	for {
		c, err := r.ReadByte()
		if err != nil {
			if err == io.EOF && i > 0 {
				return n, nil
			}
			return -1, err
		}
		k = int(hex2intTable[c])
		if k == 16 {
			if i == 0 {
				return -1, errEmptyHexNum
			}
			r.UnreadByte()
			return n, nil
		}
		if i >= maxHexIntChars {
			return -1, errTooLargeHexNum
		}
		n = (n << 4) | k
		i++
	}
}

var hexIntBufPool sync.Pool

func writeHexInt(w *bufio.Writer, n int) error {
	if n < 0 {
		panic("BUG: int must be positive")
	}

	v := hexIntBufPool.Get()
	if v == nil {
		v = make([]byte, maxHexIntChars+1)
	}
	buf := v.([]byte)
	i := len(buf) - 1
	for {
		buf[i] = int2hexbyte(n & 0xf)
		n >>= 4
		if n == 0 {
			break
		}
		i--
	}
	_, err := w.Write(buf[i:])
	hexIntBufPool.Put(v)
	return err
}

func int2hexbyte(n int) byte {
	if n < 10 {
		return '0' + byte(n)
	}
	return 'a' + byte(n) - 10
}

func hexCharUpper(c byte) byte {
	if c < 10 {
		return '0' + c
	}
	return c - 10 + 'A'
}

var hex2intTable = func() []byte {
	b := make([]byte, 256)
	for i := 0; i < 256; i++ {
		c := byte(16)
		if i >= '0' && i <= '9' {
			c = byte(i) - '0'
		} else if i >= 'a' && i <= 'f' {
			c = byte(i) - 'a' + 10
		} else if i >= 'A' && i <= 'F' {
			c = byte(i) - 'A' + 10
		}
		b[i] = c
	}
	return b
}()

const toLower = 'a' - 'A'

var toLowerTable = func() [256]byte {
	var a [256]byte
	for i := 0; i < 256; i++ {
		c := byte(i)
		if c >= 'A' && c <= 'Z' {
			c += toLower
		}
		a[i] = c
	}
	return a
}()

var toUpperTable = func() [256]byte {
	var a [256]byte
	for i := 0; i < 256; i++ {
		c := byte(i)
		if c >= 'a' && c <= 'z' {
			c -= toLower
		}
		a[i] = c
	}
	return a
}()

func lowercaseBytes(b []byte) {
	for i := 0; i < len(b); i++ {
		p := &b[i]
		*p = toLowerTable[*p]
	}
}

// b2s converts byte slice to a string without memory allocation.
// See https://groups.google.com/forum/#!msg/Golang-Nuts/ENgbUzYvCuU/90yGx7GUAgAJ .
//
// Note it may break if string and/or slice header will change
// in the future go versions.
func b2s(b []byte) string {
	return *(*string)(unsafe.Pointer(&b))
}

// s2b converts string to a byte slice without memory allocation.
//
// Note it may break if string and/or slice header will change
// in the future go versions.
func s2b(s string) []byte {
	sh := (*reflect.StringHeader)(unsafe.Pointer(&s))
	bh := reflect.SliceHeader{
		Data: sh.Data,
		Len:  sh.Len,
		Cap:  sh.Len,
	}
	return *(*[]byte)(unsafe.Pointer(&bh))
}

// AppendUnquotedArg appends url-decoded src to dst and returns appended dst.
//
// dst may point to src. In this case src will be overwritten.
func AppendUnquotedArg(dst, src []byte) []byte {
	return decodeArgAppend(dst, src)
}

// AppendQuotedArg appends url-encoded src to dst and returns appended dst.
func AppendQuotedArg(dst, src []byte) []byte {
	for _, c := range src {
		// See http://www.w3.org/TR/html5/forms.html#form-submission-algorithm
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
			c == '*' || c == '-' || c == '.' || c == '_' {
			dst = append(dst, c)
		} else {
			dst = append(dst, '%', hexCharUpper(c>>4), hexCharUpper(c&15))
		}
	}
	return dst
}

func appendQuotedPath(dst, src []byte) []byte {
	for _, c := range src {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
			c == '/' || c == '.' || c == ',' || c == '=' || c == ':' || c == '&' || c == '~' || c == '-' || c == '_' {
			dst = append(dst, c)
		} else {
			dst = append(dst, '%', hexCharUpper(c>>4), hexCharUpper(c&15))
		}
	}
	return dst
}

// EqualBytesStr returns true if string(b) == s.
//
// This function has no performance benefits comparing to string(b) == s.
// It is left here for backwards compatibility only.
//
// This function is deperecated and may be deleted soon.
func EqualBytesStr(b []byte, s string) bool {
	return string(b) == s
}

// AppendBytesStr appends src to dst and returns the extended dst.
//
// This function has no performance benefits comparing to append(dst, src...).
// It is left here for backwards compatibility only.
//
// This function is deprecated and may be deleted soon.
func AppendBytesStr(dst []byte, src string) []byte {
	return append(dst, src...)
}
