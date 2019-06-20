package fasthttp

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"sync/atomic"
	"time"
)

// ResponseHeader represents HTTP response header.
//
// It is forbidden copying ResponseHeader instances.
// Create new instances instead and use CopyTo.
//
// ResponseHeader instance MUST NOT be used from concurrently running
// goroutines.
type ResponseHeader struct {
	noCopy noCopy

	disableNormalizing bool
	noHTTP11           bool
	connectionClose    bool

	statusCode         int
	contentLength      int
	contentLengthBytes []byte

	contentType []byte
	server      []byte

	h     []argsKV
	bufKV argsKV

	cookies []argsKV
}

// RequestHeader represents HTTP request header.
//
// It is forbidden copying RequestHeader instances.
// Create new instances instead and use CopyTo.
//
// RequestHeader instance MUST NOT be used from concurrently running
// goroutines.
type RequestHeader struct {
	noCopy noCopy

	disableNormalizing bool
	noHTTP11           bool
	connectionClose    bool
	isGet              bool

	// These two fields have been moved close to other bool fields
	// for reducing RequestHeader object size.
	cookiesCollected bool
	rawHeadersParsed bool

	contentLength      int
	contentLengthBytes []byte

	method      []byte
	requestURI  []byte
	host        []byte
	contentType []byte
	userAgent   []byte

	h     []argsKV
	bufKV argsKV

	cookies []argsKV

	rawHeaders []byte
}

// SetContentRange sets 'Content-Range: bytes startPos-endPos/contentLength'
// header.
func (h *ResponseHeader) SetContentRange(startPos, endPos, contentLength int) {
	b := h.bufKV.value[:0]
	b = append(b, strBytes...)
	b = append(b, ' ')
	b = AppendUint(b, startPos)
	b = append(b, '-')
	b = AppendUint(b, endPos)
	b = append(b, '/')
	b = AppendUint(b, contentLength)
	h.bufKV.value = b

	h.SetCanonical(strContentRange, h.bufKV.value)
}

// SetByteRange sets 'Range: bytes=startPos-endPos' header.
//
//     * If startPos is negative, then 'bytes=-startPos' value is set.
//     * If endPos is negative, then 'bytes=startPos-' value is set.
func (h *RequestHeader) SetByteRange(startPos, endPos int) {
	h.parseRawHeaders()

	b := h.bufKV.value[:0]
	b = append(b, strBytes...)
	b = append(b, '=')
	if startPos >= 0 {
		b = AppendUint(b, startPos)
	} else {
		endPos = -startPos
	}
	b = append(b, '-')
	if endPos >= 0 {
		b = AppendUint(b, endPos)
	}
	h.bufKV.value = b

	h.SetCanonical(strRange, h.bufKV.value)
}

// StatusCode returns response status code.
func (h *ResponseHeader) StatusCode() int {
	if h.statusCode == 0 {
		return StatusOK
	}
	return h.statusCode
}

// SetStatusCode sets response status code.
func (h *ResponseHeader) SetStatusCode(statusCode int) {
	h.statusCode = statusCode
}

// SetLastModified sets 'Last-Modified' header to the given value.
func (h *ResponseHeader) SetLastModified(t time.Time) {
	h.bufKV.value = AppendHTTPDate(h.bufKV.value[:0], t)
	h.SetCanonical(strLastModified, h.bufKV.value)
}

// ConnectionClose returns true if 'Connection: close' header is set.
func (h *ResponseHeader) ConnectionClose() bool {
	return h.connectionClose
}

// SetConnectionClose sets 'Connection: close' header.
func (h *ResponseHeader) SetConnectionClose() {
	h.connectionClose = true
}

// ResetConnectionClose clears 'Connection: close' header if it exists.
func (h *ResponseHeader) ResetConnectionClose() {
	if h.connectionClose {
		h.connectionClose = false
		h.h = delAllArgsBytes(h.h, strConnection)
	}
}

// ConnectionClose returns true if 'Connection: close' header is set.
func (h *RequestHeader) ConnectionClose() bool {
	h.parseRawHeaders()
	return h.connectionClose
}

func (h *RequestHeader) connectionCloseFast() bool {
	// h.parseRawHeaders() isn't called for performance reasons.
	// Use ConnectionClose for triggering raw headers parsing.
	return h.connectionClose
}

// SetConnectionClose sets 'Connection: close' header.
func (h *RequestHeader) SetConnectionClose() {
	// h.parseRawHeaders() isn't called for performance reasons.
	h.connectionClose = true
}

// ResetConnectionClose clears 'Connection: close' header if it exists.
func (h *RequestHeader) ResetConnectionClose() {
	h.parseRawHeaders()
	if h.connectionClose {
		h.connectionClose = false
		h.h = delAllArgsBytes(h.h, strConnection)
	}
}

// ConnectionUpgrade returns true if 'Connection: Upgrade' header is set.
func (h *ResponseHeader) ConnectionUpgrade() bool {
	return hasHeaderValue(h.Peek("Connection"), strUpgrade)
}

// ConnectionUpgrade returns true if 'Connection: Upgrade' header is set.
func (h *RequestHeader) ConnectionUpgrade() bool {
	h.parseRawHeaders()
	return hasHeaderValue(h.Peek("Connection"), strUpgrade)
}

// ContentLength returns Content-Length header value.
//
// It may be negative:
// -1 means Transfer-Encoding: chunked.
// -2 means Transfer-Encoding: identity.
func (h *ResponseHeader) ContentLength() int {
	return h.contentLength
}

// SetContentLength sets Content-Length header value.
//
// Content-Length may be negative:
// -1 means Transfer-Encoding: chunked.
// -2 means Transfer-Encoding: identity.
func (h *ResponseHeader) SetContentLength(contentLength int) {
	if h.mustSkipContentLength() {
		return
	}
	h.contentLength = contentLength
	if contentLength >= 0 {
		h.contentLengthBytes = AppendUint(h.contentLengthBytes[:0], contentLength)
		h.h = delAllArgsBytes(h.h, strTransferEncoding)
	} else {
		h.contentLengthBytes = h.contentLengthBytes[:0]
		value := strChunked
		if contentLength == -2 {
			h.SetConnectionClose()
			value = strIdentity
		}
		h.h = setArgBytes(h.h, strTransferEncoding, value)
	}
}

func (h *ResponseHeader) mustSkipContentLength() bool {
	// From http/1.1 specs:
	// All 1xx (informational), 204 (no content), and 304 (not modified) responses MUST NOT include a message-body
	statusCode := h.StatusCode()

	// Fast path.
	if statusCode < 100 || statusCode == StatusOK {
		return false
	}

	// Slow path.
	return statusCode == StatusNotModified || statusCode == StatusNoContent || statusCode < 200
}

// ContentLength returns Content-Length header value.
//
// It may be negative:
// -1 means Transfer-Encoding: chunked.
func (h *RequestHeader) ContentLength() int {
	if h.noBody() {
		return 0
	}
	h.parseRawHeaders()
	return h.contentLength
}

// SetContentLength sets Content-Length header value.
//
// Negative content-length sets 'Transfer-Encoding: chunked' header.
func (h *RequestHeader) SetContentLength(contentLength int) {
	h.parseRawHeaders()
	h.contentLength = contentLength
	if contentLength >= 0 {
		h.contentLengthBytes = AppendUint(h.contentLengthBytes[:0], contentLength)
		h.h = delAllArgsBytes(h.h, strTransferEncoding)
	} else {
		h.contentLengthBytes = h.contentLengthBytes[:0]
		h.h = setArgBytes(h.h, strTransferEncoding, strChunked)
	}
}

func (h *ResponseHeader) isCompressibleContentType() bool {
	contentType := h.ContentType()
	return bytes.HasPrefix(contentType, strTextSlash) ||
		bytes.HasPrefix(contentType, strApplicationSlash)
}

// ContentType returns Content-Type header value.
func (h *ResponseHeader) ContentType() []byte {
	contentType := h.contentType
	if len(h.contentType) == 0 {
		contentType = defaultContentType
	}
	return contentType
}

// SetContentType sets Content-Type header value.
func (h *ResponseHeader) SetContentType(contentType string) {
	h.contentType = append(h.contentType[:0], contentType...)
}

// SetContentTypeBytes sets Content-Type header value.
func (h *ResponseHeader) SetContentTypeBytes(contentType []byte) {
	h.contentType = append(h.contentType[:0], contentType...)
}

// Server returns Server header value.
func (h *ResponseHeader) Server() []byte {
	return h.server
}

// SetServer sets Server header value.
func (h *ResponseHeader) SetServer(server string) {
	h.server = append(h.server[:0], server...)
}

// SetServerBytes sets Server header value.
func (h *ResponseHeader) SetServerBytes(server []byte) {
	h.server = append(h.server[:0], server...)
}

// ContentType returns Content-Type header value.
func (h *RequestHeader) ContentType() []byte {
	h.parseRawHeaders()
	return h.contentType
}

// SetContentType sets Content-Type header value.
func (h *RequestHeader) SetContentType(contentType string) {
	h.parseRawHeaders()
	h.contentType = append(h.contentType[:0], contentType...)
}

// SetContentTypeBytes sets Content-Type header value.
func (h *RequestHeader) SetContentTypeBytes(contentType []byte) {
	h.parseRawHeaders()
	h.contentType = append(h.contentType[:0], contentType...)
}

// SetMultipartFormBoundary sets the following Content-Type:
// 'multipart/form-data; boundary=...'
// where ... is substituted by the given boundary.
func (h *RequestHeader) SetMultipartFormBoundary(boundary string) {
	h.parseRawHeaders()

	b := h.bufKV.value[:0]
	b = append(b, strMultipartFormData...)
	b = append(b, ';', ' ')
	b = append(b, strBoundary...)
	b = append(b, '=')
	b = append(b, boundary...)
	h.bufKV.value = b

	h.SetContentTypeBytes(h.bufKV.value)
}

// SetMultipartFormBoundaryBytes sets the following Content-Type:
// 'multipart/form-data; boundary=...'
// where ... is substituted by the given boundary.
func (h *RequestHeader) SetMultipartFormBoundaryBytes(boundary []byte) {
	h.parseRawHeaders()

	b := h.bufKV.value[:0]
	b = append(b, strMultipartFormData...)
	b = append(b, ';', ' ')
	b = append(b, strBoundary...)
	b = append(b, '=')
	b = append(b, boundary...)
	h.bufKV.value = b

	h.SetContentTypeBytes(h.bufKV.value)
}

// MultipartFormBoundary returns boundary part
// from 'multipart/form-data; boundary=...' Content-Type.
func (h *RequestHeader) MultipartFormBoundary() []byte {
	b := h.ContentType()
	if !bytes.HasPrefix(b, strMultipartFormData) {
		return nil
	}
	b = b[len(strMultipartFormData):]
	if len(b) == 0 || b[0] != ';' {
		return nil
	}

	var n int
	for len(b) > 0 {
		n++
		for len(b) > n && b[n] == ' ' {
			n++
		}
		b = b[n:]
		if !bytes.HasPrefix(b, strBoundary) {
			if n = bytes.IndexByte(b, ';'); n < 0 {
				return nil
			}
			continue
		}

		b = b[len(strBoundary):]
		if len(b) == 0 || b[0] != '=' {
			return nil
		}
		b = b[1:]
		if n = bytes.IndexByte(b, ';'); n >= 0 {
			b = b[:n]
		}
		if len(b) > 1 && b[0] == '"' && b[len(b)-1] == '"' {
			b = b[1 : len(b)-1]
		}
		return b
	}
	return nil
}

// Host returns Host header value.
func (h *RequestHeader) Host() []byte {
	if len(h.host) > 0 {
		return h.host
	}
	if !h.rawHeadersParsed {
		// fast path without employing full headers parsing.
		host := peekRawHeader(h.rawHeaders, strHost)
		if len(host) > 0 {
			h.host = append(h.host[:0], host...)
			return h.host
		}
	}

	// slow path.
	h.parseRawHeaders()
	return h.host
}

// SetHost sets Host header value.
func (h *RequestHeader) SetHost(host string) {
	h.parseRawHeaders()
	h.host = append(h.host[:0], host...)
}

// SetHostBytes sets Host header value.
func (h *RequestHeader) SetHostBytes(host []byte) {
	h.parseRawHeaders()
	h.host = append(h.host[:0], host...)
}

// UserAgent returns User-Agent header value.
func (h *RequestHeader) UserAgent() []byte {
	h.parseRawHeaders()
	return h.userAgent
}

// SetUserAgent sets User-Agent header value.
func (h *RequestHeader) SetUserAgent(userAgent string) {
	h.parseRawHeaders()
	h.userAgent = append(h.userAgent[:0], userAgent...)
}

// SetUserAgentBytes sets User-Agent header value.
func (h *RequestHeader) SetUserAgentBytes(userAgent []byte) {
	h.parseRawHeaders()
	h.userAgent = append(h.userAgent[:0], userAgent...)
}

// Referer returns Referer header value.
func (h *RequestHeader) Referer() []byte {
	return h.PeekBytes(strReferer)
}

// SetReferer sets Referer header value.
func (h *RequestHeader) SetReferer(referer string) {
	h.SetBytesK(strReferer, referer)
}

// SetRefererBytes sets Referer header value.
func (h *RequestHeader) SetRefererBytes(referer []byte) {
	h.SetCanonical(strReferer, referer)
}

// Method returns HTTP request method.
func (h *RequestHeader) Method() []byte {
	if len(h.method) == 0 {
		return strGet
	}
	return h.method
}

// SetMethod sets HTTP request method.
func (h *RequestHeader) SetMethod(method string) {
	h.method = append(h.method[:0], method...)
}

// SetMethodBytes sets HTTP request method.
func (h *RequestHeader) SetMethodBytes(method []byte) {
	h.method = append(h.method[:0], method...)
}

// RequestURI returns RequestURI from the first HTTP request line.
func (h *RequestHeader) RequestURI() []byte {
	requestURI := h.requestURI
	if len(requestURI) == 0 {
		requestURI = strSlash
	}
	return requestURI
}

// SetRequestURI sets RequestURI for the first HTTP request line.
// RequestURI must be properly encoded.
// Use URI.RequestURI for constructing proper RequestURI if unsure.
func (h *RequestHeader) SetRequestURI(requestURI string) {
	h.requestURI = append(h.requestURI[:0], requestURI...)
}

// SetRequestURIBytes sets RequestURI for the first HTTP request line.
// RequestURI must be properly encoded.
// Use URI.RequestURI for constructing proper RequestURI if unsure.
func (h *RequestHeader) SetRequestURIBytes(requestURI []byte) {
	h.requestURI = append(h.requestURI[:0], requestURI...)
}

// IsGet returns true if request method is GET.
func (h *RequestHeader) IsGet() bool {
	// Optimize fast path for GET requests.
	if !h.isGet {
		h.isGet = bytes.Equal(h.Method(), strGet)
	}
	return h.isGet
}

// IsPost returns true if request methos is POST.
func (h *RequestHeader) IsPost() bool {
	return bytes.Equal(h.Method(), strPost)
}

// IsPut returns true if request method is PUT.
func (h *RequestHeader) IsPut() bool {
	return bytes.Equal(h.Method(), strPut)
}

// IsHead returns true if request method is HEAD.
func (h *RequestHeader) IsHead() bool {
	// Fast path
	if h.isGet {
		return false
	}
	return bytes.Equal(h.Method(), strHead)
}

// IsDelete returns true if request method is DELETE.
func (h *RequestHeader) IsDelete() bool {
	return bytes.Equal(h.Method(), strDelete)
}

// IsHTTP11 returns true if the request is HTTP/1.1.
func (h *RequestHeader) IsHTTP11() bool {
	return !h.noHTTP11
}

// IsHTTP11 returns true if the response is HTTP/1.1.
func (h *ResponseHeader) IsHTTP11() bool {
	return !h.noHTTP11
}

// HasAcceptEncoding returns true if the header contains
// the given Accept-Encoding value.
func (h *RequestHeader) HasAcceptEncoding(acceptEncoding string) bool {
	h.bufKV.value = append(h.bufKV.value[:0], acceptEncoding...)
	return h.HasAcceptEncodingBytes(h.bufKV.value)
}

// HasAcceptEncodingBytes returns true if the header contains
// the given Accept-Encoding value.
func (h *RequestHeader) HasAcceptEncodingBytes(acceptEncoding []byte) bool {
	ae := h.peek(strAcceptEncoding)
	n := bytes.Index(ae, acceptEncoding)
	if n < 0 {
		return false
	}
	b := ae[n+len(acceptEncoding):]
	if len(b) > 0 && b[0] != ',' {
		return false
	}
	if n == 0 {
		return true
	}
	return ae[n-1] == ' '
}

// Len returns the number of headers set,
// i.e. the number of times f is called in VisitAll.
func (h *ResponseHeader) Len() int {
	n := 0
	h.VisitAll(func(k, v []byte) { n++ })
	return n
}

// Len returns the number of headers set,
// i.e. the number of times f is called in VisitAll.
func (h *RequestHeader) Len() int {
	n := 0
	h.VisitAll(func(k, v []byte) { n++ })
	return n
}

// DisableNormalizing disables header names' normalization.
//
// By default all the header names are normalized by uppercasing
// the first letter and all the first letters following dashes,
// while lowercasing all the other letters.
// Examples:
//
//     * CONNECTION -> Connection
//     * conteNT-tYPE -> Content-Type
//     * foo-bar-baz -> Foo-Bar-Baz
//
// Disable header names' normalization only if know what are you doing.
func (h *RequestHeader) DisableNormalizing() {
	h.disableNormalizing = true
}

// DisableNormalizing disables header names' normalization.
//
// By default all the header names are normalized by uppercasing
// the first letter and all the first letters following dashes,
// while lowercasing all the other letters.
// Examples:
//
//     * CONNECTION -> Connection
//     * conteNT-tYPE -> Content-Type
//     * foo-bar-baz -> Foo-Bar-Baz
//
// Disable header names' normalization only if know what are you doing.
func (h *ResponseHeader) DisableNormalizing() {
	h.disableNormalizing = true
}

// Reset clears response header.
func (h *ResponseHeader) Reset() {
	h.disableNormalizing = false
	h.resetSkipNormalize()
}

func (h *ResponseHeader) resetSkipNormalize() {
	h.noHTTP11 = false
	h.connectionClose = false

	h.statusCode = 0
	h.contentLength = 0
	h.contentLengthBytes = h.contentLengthBytes[:0]

	h.contentType = h.contentType[:0]
	h.server = h.server[:0]

	h.h = h.h[:0]
	h.cookies = h.cookies[:0]
}

// Reset clears request header.
func (h *RequestHeader) Reset() {
	h.disableNormalizing = false
	h.resetSkipNormalize()
}

func (h *RequestHeader) resetSkipNormalize() {
	h.noHTTP11 = false
	h.connectionClose = false
	h.isGet = false

	h.contentLength = 0
	h.contentLengthBytes = h.contentLengthBytes[:0]

	h.method = h.method[:0]
	h.requestURI = h.requestURI[:0]
	h.host = h.host[:0]
	h.contentType = h.contentType[:0]
	h.userAgent = h.userAgent[:0]

	h.h = h.h[:0]
	h.cookies = h.cookies[:0]
	h.cookiesCollected = false

	h.rawHeaders = h.rawHeaders[:0]
	h.rawHeadersParsed = false
}

// CopyTo copies all the headers to dst.
func (h *ResponseHeader) CopyTo(dst *ResponseHeader) {
	dst.Reset()

	dst.disableNormalizing = h.disableNormalizing
	dst.noHTTP11 = h.noHTTP11
	dst.connectionClose = h.connectionClose

	dst.statusCode = h.statusCode
	dst.contentLength = h.contentLength
	dst.contentLengthBytes = append(dst.contentLengthBytes[:0], h.contentLengthBytes...)
	dst.contentType = append(dst.contentType[:0], h.contentType...)
	dst.server = append(dst.server[:0], h.server...)
	dst.h = copyArgs(dst.h, h.h)
	dst.cookies = copyArgs(dst.cookies, h.cookies)
}

// CopyTo copies all the headers to dst.
func (h *RequestHeader) CopyTo(dst *RequestHeader) {
	dst.Reset()

	dst.disableNormalizing = h.disableNormalizing
	dst.noHTTP11 = h.noHTTP11
	dst.connectionClose = h.connectionClose
	dst.isGet = h.isGet

	dst.contentLength = h.contentLength
	dst.contentLengthBytes = append(dst.contentLengthBytes[:0], h.contentLengthBytes...)
	dst.method = append(dst.method[:0], h.method...)
	dst.requestURI = append(dst.requestURI[:0], h.requestURI...)
	dst.host = append(dst.host[:0], h.host...)
	dst.contentType = append(dst.contentType[:0], h.contentType...)
	dst.userAgent = append(dst.userAgent[:0], h.userAgent...)
	dst.h = copyArgs(dst.h, h.h)
	dst.cookies = copyArgs(dst.cookies, h.cookies)
	dst.cookiesCollected = h.cookiesCollected
	dst.rawHeaders = append(dst.rawHeaders[:0], h.rawHeaders...)
	dst.rawHeadersParsed = h.rawHeadersParsed
}

// VisitAll calls f for each header.
//
// f must not retain references to key and/or value after returning.
// Copy key and/or value contents before returning if you need retaining them.
func (h *ResponseHeader) VisitAll(f func(key, value []byte)) {
	if len(h.contentLengthBytes) > 0 {
		f(strContentLength, h.contentLengthBytes)
	}
	contentType := h.ContentType()
	if len(contentType) > 0 {
		f(strContentType, contentType)
	}
	server := h.Server()
	if len(server) > 0 {
		f(strServer, server)
	}
	if len(h.cookies) > 0 {
		visitArgs(h.cookies, func(k, v []byte) {
			f(strSetCookie, v)
		})
	}
	visitArgs(h.h, f)
	if h.ConnectionClose() {
		f(strConnection, strClose)
	}
}

// VisitAllCookie calls f for each response cookie.
//
// Cookie name is passed in key and the whole Set-Cookie header value
// is passed in value on each f invocation. Value may be parsed
// with Cookie.ParseBytes().
//
// f must not retain references to key and/or value after returning.
func (h *ResponseHeader) VisitAllCookie(f func(key, value []byte)) {
	visitArgs(h.cookies, f)
}

// VisitAllCookie calls f for each request cookie.
//
// f must not retain references to key and/or value after returning.
func (h *RequestHeader) VisitAllCookie(f func(key, value []byte)) {
	h.parseRawHeaders()
	h.collectCookies()
	visitArgs(h.cookies, f)
}

// VisitAll calls f for each header.
//
// f must not retain references to key and/or value after returning.
// Copy key and/or value contents before returning if you need retaining them.
func (h *RequestHeader) VisitAll(f func(key, value []byte)) {
	h.parseRawHeaders()
	host := h.Host()
	if len(host) > 0 {
		f(strHost, host)
	}
	if len(h.contentLengthBytes) > 0 {
		f(strContentLength, h.contentLengthBytes)
	}
	contentType := h.ContentType()
	if len(contentType) > 0 {
		f(strContentType, contentType)
	}
	userAgent := h.UserAgent()
	if len(userAgent) > 0 {
		f(strUserAgent, userAgent)
	}

	h.collectCookies()
	if len(h.cookies) > 0 {
		h.bufKV.value = appendRequestCookieBytes(h.bufKV.value[:0], h.cookies)
		f(strCookie, h.bufKV.value)
	}
	visitArgs(h.h, f)
	if h.ConnectionClose() {
		f(strConnection, strClose)
	}
}

// Del deletes header with the given key.
func (h *ResponseHeader) Del(key string) {
	k := getHeaderKeyBytes(&h.bufKV, key, h.disableNormalizing)
	h.del(k)
}

// DelBytes deletes header with the given key.
func (h *ResponseHeader) DelBytes(key []byte) {
	h.bufKV.key = append(h.bufKV.key[:0], key...)
	normalizeHeaderKey(h.bufKV.key, h.disableNormalizing)
	h.del(h.bufKV.key)
}

func (h *ResponseHeader) del(key []byte) {
	switch string(key) {
	case "Content-Type":
		h.contentType = h.contentType[:0]
	case "Server":
		h.server = h.server[:0]
	case "Set-Cookie":
		h.cookies = h.cookies[:0]
	case "Content-Length":
		h.contentLength = 0
		h.contentLengthBytes = h.contentLengthBytes[:0]
	case "Connection":
		h.connectionClose = false
	}
	h.h = delAllArgsBytes(h.h, key)
}

// Del deletes header with the given key.
func (h *RequestHeader) Del(key string) {
	h.parseRawHeaders()
	k := getHeaderKeyBytes(&h.bufKV, key, h.disableNormalizing)
	h.del(k)
}

// DelBytes deletes header with the given key.
func (h *RequestHeader) DelBytes(key []byte) {
	h.parseRawHeaders()
	h.bufKV.key = append(h.bufKV.key[:0], key...)
	normalizeHeaderKey(h.bufKV.key, h.disableNormalizing)
	h.del(h.bufKV.key)
}

func (h *RequestHeader) del(key []byte) {
	switch string(key) {
	case "Host":
		h.host = h.host[:0]
	case "Content-Type":
		h.contentType = h.contentType[:0]
	case "User-Agent":
		h.userAgent = h.userAgent[:0]
	case "Cookie":
		h.cookies = h.cookies[:0]
	case "Content-Length":
		h.contentLength = 0
		h.contentLengthBytes = h.contentLengthBytes[:0]
	case "Connection":
		h.connectionClose = false
	}
	h.h = delAllArgsBytes(h.h, key)
}

// Add adds the given 'key: value' header.
//
// Multiple headers with the same key may be added with this function.
// Use Set for setting a single header for the given key.
func (h *ResponseHeader) Add(key, value string) {
	k := getHeaderKeyBytes(&h.bufKV, key, h.disableNormalizing)
	h.h = appendArg(h.h, b2s(k), value)
}

// AddBytesK adds the given 'key: value' header.
//
// Multiple headers with the same key may be added with this function.
// Use SetBytesK for setting a single header for the given key.
func (h *ResponseHeader) AddBytesK(key []byte, value string) {
	h.Add(b2s(key), value)
}

// AddBytesV adds the given 'key: value' header.
//
// Multiple headers with the same key may be added with this function.
// Use SetBytesV for setting a single header for the given key.
func (h *ResponseHeader) AddBytesV(key string, value []byte) {
	h.Add(key, b2s(value))
}

// AddBytesKV adds the given 'key: value' header.
//
// Multiple headers with the same key may be added with this function.
// Use SetBytesKV for setting a single header for the given key.
func (h *ResponseHeader) AddBytesKV(key, value []byte) {
	h.Add(b2s(key), b2s(value))
}

// Set sets the given 'key: value' header.
//
// Use Add for setting multiple header values under the same key.
func (h *ResponseHeader) Set(key, value string) {
	initHeaderKV(&h.bufKV, key, value, h.disableNormalizing)
	h.SetCanonical(h.bufKV.key, h.bufKV.value)
}

// SetBytesK sets the given 'key: value' header.
//
// Use AddBytesK for setting multiple header values under the same key.
func (h *ResponseHeader) SetBytesK(key []byte, value string) {
	h.bufKV.value = append(h.bufKV.value[:0], value...)
	h.SetBytesKV(key, h.bufKV.value)
}

// SetBytesV sets the given 'key: value' header.
//
// Use AddBytesV for setting multiple header values under the same key.
func (h *ResponseHeader) SetBytesV(key string, value []byte) {
	k := getHeaderKeyBytes(&h.bufKV, key, h.disableNormalizing)
	h.SetCanonical(k, value)
}

// SetBytesKV sets the given 'key: value' header.
//
// Use AddBytesKV for setting multiple header values under the same key.
func (h *ResponseHeader) SetBytesKV(key, value []byte) {
	h.bufKV.key = append(h.bufKV.key[:0], key...)
	normalizeHeaderKey(h.bufKV.key, h.disableNormalizing)
	h.SetCanonical(h.bufKV.key, value)
}

// SetCanonical sets the given 'key: value' header assuming that
// key is in canonical form.
func (h *ResponseHeader) SetCanonical(key, value []byte) {
	switch string(key) {
	case "Content-Type":
		h.SetContentTypeBytes(value)
	case "Server":
		h.SetServerBytes(value)
	case "Set-Cookie":
		var kv *argsKV
		h.cookies, kv = allocArg(h.cookies)
		kv.key = getCookieKey(kv.key, value)
		kv.value = append(kv.value[:0], value...)
	case "Content-Length":
		if contentLength, err := parseContentLength(value); err == nil {
			h.contentLength = contentLength
			h.contentLengthBytes = append(h.contentLengthBytes[:0], value...)
		}
	case "Connection":
		if bytes.Equal(strClose, value) {
			h.SetConnectionClose()
		} else {
			h.ResetConnectionClose()
			h.h = setArgBytes(h.h, key, value)
		}
	case "Transfer-Encoding":
		// Transfer-Encoding is managed automatically.
	case "Date":
		// Date is managed automatically.
	default:
		h.h = setArgBytes(h.h, key, value)
	}
}

// SetCookie sets the given response cookie.
//
// It is save re-using the cookie after the function returns.
func (h *ResponseHeader) SetCookie(cookie *Cookie) {
	h.cookies = setArgBytes(h.cookies, cookie.Key(), cookie.Cookie())
}

// SetCookie sets 'key: value' cookies.
func (h *RequestHeader) SetCookie(key, value string) {
	h.parseRawHeaders()
	h.collectCookies()
	h.cookies = setArg(h.cookies, key, value)
}

// SetCookieBytesK sets 'key: value' cookies.
func (h *RequestHeader) SetCookieBytesK(key []byte, value string) {
	h.SetCookie(b2s(key), value)
}

// SetCookieBytesKV sets 'key: value' cookies.
func (h *RequestHeader) SetCookieBytesKV(key, value []byte) {
	h.SetCookie(b2s(key), b2s(value))
}

// DelClientCookie instructs the client to remove the given cookie.
//
// Use DelCookie if you want just removing the cookie from response header.
func (h *ResponseHeader) DelClientCookie(key string) {
	h.DelCookie(key)

	c := AcquireCookie()
	c.SetKey(key)
	c.SetExpire(CookieExpireDelete)
	h.SetCookie(c)
	ReleaseCookie(c)
}

// DelClientCookieBytes instructs the client to remove the given cookie.
//
// Use DelCookieBytes if you want just removing the cookie from response header.
func (h *ResponseHeader) DelClientCookieBytes(key []byte) {
	h.DelClientCookie(b2s(key))
}

// DelCookie removes cookie under the given key from response header.
//
// Note that DelCookie doesn't remove the cookie from the client.
// Use DelClientCookie instead.
func (h *ResponseHeader) DelCookie(key string) {
	h.cookies = delAllArgs(h.cookies, key)
}

// DelCookieBytes removes cookie under the given key from response header.
//
// Note that DelCookieBytes doesn't remove the cookie from the client.
// Use DelClientCookieBytes instead.
func (h *ResponseHeader) DelCookieBytes(key []byte) {
	h.DelCookie(b2s(key))
}

// DelCookie removes cookie under the given key.
func (h *RequestHeader) DelCookie(key string) {
	h.parseRawHeaders()
	h.collectCookies()
	h.cookies = delAllArgs(h.cookies, key)
}

// DelCookieBytes removes cookie under the given key.
func (h *RequestHeader) DelCookieBytes(key []byte) {
	h.DelCookie(b2s(key))
}

// DelAllCookies removes all the cookies from response headers.
func (h *ResponseHeader) DelAllCookies() {
	h.cookies = h.cookies[:0]
}

// DelAllCookies removes all the cookies from request headers.
func (h *RequestHeader) DelAllCookies() {
	h.parseRawHeaders()
	h.collectCookies()
	h.cookies = h.cookies[:0]
}

// Add adds the given 'key: value' header.
//
// Multiple headers with the same key may be added with this function.
// Use Set for setting a single header for the given key.
func (h *RequestHeader) Add(key, value string) {
	k := getHeaderKeyBytes(&h.bufKV, key, h.disableNormalizing)
	h.h = appendArg(h.h, b2s(k), value)
}

// AddBytesK adds the given 'key: value' header.
//
// Multiple headers with the same key may be added with this function.
// Use SetBytesK for setting a single header for the given key.
func (h *RequestHeader) AddBytesK(key []byte, value string) {
	h.Add(b2s(key), value)
}

// AddBytesV adds the given 'key: value' header.
//
// Multiple headers with the same key may be added with this function.
// Use SetBytesV for setting a single header for the given key.
func (h *RequestHeader) AddBytesV(key string, value []byte) {
	h.Add(key, b2s(value))
}

// AddBytesKV adds the given 'key: value' header.
//
// Multiple headers with the same key may be added with this function.
// Use SetBytesKV for setting a single header for the given key.
func (h *RequestHeader) AddBytesKV(key, value []byte) {
	h.Add(b2s(key), b2s(value))
}

// Set sets the given 'key: value' header.
//
// Use Add for setting multiple header values under the same key.
func (h *RequestHeader) Set(key, value string) {
	initHeaderKV(&h.bufKV, key, value, h.disableNormalizing)
	h.SetCanonical(h.bufKV.key, h.bufKV.value)
}

// SetBytesK sets the given 'key: value' header.
//
// Use AddBytesK for setting multiple header values under the same key.
func (h *RequestHeader) SetBytesK(key []byte, value string) {
	h.bufKV.value = append(h.bufKV.value[:0], value...)
	h.SetBytesKV(key, h.bufKV.value)
}

// SetBytesV sets the given 'key: value' header.
//
// Use AddBytesV for setting multiple header values under the same key.
func (h *RequestHeader) SetBytesV(key string, value []byte) {
	k := getHeaderKeyBytes(&h.bufKV, key, h.disableNormalizing)
	h.SetCanonical(k, value)
}

// SetBytesKV sets the given 'key: value' header.
//
// Use AddBytesKV for setting multiple header values under the same key.
func (h *RequestHeader) SetBytesKV(key, value []byte) {
	h.bufKV.key = append(h.bufKV.key[:0], key...)
	normalizeHeaderKey(h.bufKV.key, h.disableNormalizing)
	h.SetCanonical(h.bufKV.key, value)
}

// SetCanonical sets the given 'key: value' header assuming that
// key is in canonical form.
func (h *RequestHeader) SetCanonical(key, value []byte) {
	h.parseRawHeaders()
	switch string(key) {
	case "Host":
		h.SetHostBytes(value)
	case "Content-Type":
		h.SetContentTypeBytes(value)
	case "User-Agent":
		h.SetUserAgentBytes(value)
	case "Cookie":
		h.collectCookies()
		h.cookies = parseRequestCookies(h.cookies, value)
	case "Content-Length":
		if contentLength, err := parseContentLength(value); err == nil {
			h.contentLength = contentLength
			h.contentLengthBytes = append(h.contentLengthBytes[:0], value...)
		}
	case "Connection":
		if bytes.Equal(strClose, value) {
			h.SetConnectionClose()
		} else {
			h.ResetConnectionClose()
			h.h = setArgBytes(h.h, key, value)
		}
	case "Transfer-Encoding":
		// Transfer-Encoding is managed automatically.
	default:
		h.h = setArgBytes(h.h, key, value)
	}
}

// Peek returns header value for the given key.
//
// Returned value is valid until the next call to ResponseHeader.
// Do not store references to returned value. Make copies instead.
func (h *ResponseHeader) Peek(key string) []byte {
	k := getHeaderKeyBytes(&h.bufKV, key, h.disableNormalizing)
	return h.peek(k)
}

// PeekBytes returns header value for the given key.
//
// Returned value is valid until the next call to ResponseHeader.
// Do not store references to returned value. Make copies instead.
func (h *ResponseHeader) PeekBytes(key []byte) []byte {
	h.bufKV.key = append(h.bufKV.key[:0], key...)
	normalizeHeaderKey(h.bufKV.key, h.disableNormalizing)
	return h.peek(h.bufKV.key)
}

// Peek returns header value for the given key.
//
// Returned value is valid until the next call to RequestHeader.
// Do not store references to returned value. Make copies instead.
func (h *RequestHeader) Peek(key string) []byte {
	k := getHeaderKeyBytes(&h.bufKV, key, h.disableNormalizing)
	return h.peek(k)
}

// PeekBytes returns header value for the given key.
//
// Returned value is valid until the next call to RequestHeader.
// Do not store references to returned value. Make copies instead.
func (h *RequestHeader) PeekBytes(key []byte) []byte {
	h.bufKV.key = append(h.bufKV.key[:0], key...)
	normalizeHeaderKey(h.bufKV.key, h.disableNormalizing)
	return h.peek(h.bufKV.key)
}

func (h *ResponseHeader) peek(key []byte) []byte {
	switch string(key) {
	case "Content-Type":
		return h.ContentType()
	case "Server":
		return h.Server()
	case "Connection":
		if h.ConnectionClose() {
			return strClose
		}
		return peekArgBytes(h.h, key)
	case "Content-Length":
		return h.contentLengthBytes
	default:
		return peekArgBytes(h.h, key)
	}
}

func (h *RequestHeader) peek(key []byte) []byte {
	h.parseRawHeaders()
	switch string(key) {
	case "Host":
		return h.Host()
	case "Content-Type":
		return h.ContentType()
	case "User-Agent":
		return h.UserAgent()
	case "Connection":
		if h.ConnectionClose() {
			return strClose
		}
		return peekArgBytes(h.h, key)
	case "Content-Length":
		return h.contentLengthBytes
	default:
		return peekArgBytes(h.h, key)
	}
}

// Cookie returns cookie for the given key.
func (h *RequestHeader) Cookie(key string) []byte {
	h.parseRawHeaders()
	h.collectCookies()
	return peekArgStr(h.cookies, key)
}

// CookieBytes returns cookie for the given key.
func (h *RequestHeader) CookieBytes(key []byte) []byte {
	h.parseRawHeaders()
	h.collectCookies()
	return peekArgBytes(h.cookies, key)
}

// Cookie fills cookie for the given cookie.Key.
//
// Returns false if cookie with the given cookie.Key is missing.
func (h *ResponseHeader) Cookie(cookie *Cookie) bool {
	v := peekArgBytes(h.cookies, cookie.Key())
	if v == nil {
		return false
	}
	cookie.ParseBytes(v)
	return true
}

// Read reads response header from r.
//
// io.EOF is returned if r is closed before reading the first header byte.
func (h *ResponseHeader) Read(r *bufio.Reader) error {
	n := 1
	for {
		err := h.tryRead(r, n)
		if err == nil {
			return nil
		}
		if err != errNeedMore {
			h.resetSkipNormalize()
			return err
		}
		n = r.Buffered() + 1
	}
}

func (h *ResponseHeader) tryRead(r *bufio.Reader, n int) error {
	h.resetSkipNormalize()
	b, err := r.Peek(n)
	if len(b) == 0 {
		// treat all errors on the first byte read as EOF
		if n == 1 || err == io.EOF {
			return io.EOF
		}

		// This is for go 1.6 bug. See https://github.com/golang/go/issues/14121 .
		if err == bufio.ErrBufferFull {
			return &ErrSmallBuffer{
				error: fmt.Errorf("error when reading response headers: %s", errSmallBuffer),
			}
		}

		return fmt.Errorf("error when reading response headers: %s", err)
	}
	b = mustPeekBuffered(r)
	headersLen, errParse := h.parse(b)
	if errParse != nil {
		return headerError("response", err, errParse, b)
	}
	mustDiscard(r, headersLen)
	return nil
}

func headerError(typ string, err, errParse error, b []byte) error {
	if errParse != errNeedMore {
		return headerErrorMsg(typ, errParse, b)
	}
	if err == nil {
		return errNeedMore
	}

	// Buggy servers may leave trailing CRLFs after http body.
	// Treat this case as EOF.
	if isOnlyCRLF(b) {
		return io.EOF
	}

	if err != bufio.ErrBufferFull {
		return headerErrorMsg(typ, err, b)
	}
	return &ErrSmallBuffer{
		error: headerErrorMsg(typ, errSmallBuffer, b),
	}
}

func headerErrorMsg(typ string, err error, b []byte) error {
	return fmt.Errorf("error when reading %s headers: %s. Buffer size=%d, contents: %s", typ, err, len(b), bufferSnippet(b))
}

// Read reads request header from r.
//
// io.EOF is returned if r is closed before reading the first header byte.
func (h *RequestHeader) Read(r *bufio.Reader) error {
	n := 1
	for {
		err := h.tryRead(r, n)
		if err == nil {
			return nil
		}
		if err != errNeedMore {
			h.resetSkipNormalize()
			return err
		}
		n = r.Buffered() + 1
	}
}

func (h *RequestHeader) tryRead(r *bufio.Reader, n int) error {
	h.resetSkipNormalize()
	b, err := r.Peek(n)
	if len(b) == 0 {
		// treat all errors on the first byte read as EOF
		if n == 1 || err == io.EOF {
			return io.EOF
		}

		// This is for go 1.6 bug. See https://github.com/golang/go/issues/14121 .
		if err == bufio.ErrBufferFull {
			return &ErrSmallBuffer{
				error: fmt.Errorf("error when reading request headers: %s", errSmallBuffer),
			}
		}

		return fmt.Errorf("error when reading request headers: %s", err)
	}
	b = mustPeekBuffered(r)
	headersLen, errParse := h.parse(b)
	if errParse != nil {
		return headerError("request", err, errParse, b)
	}
	mustDiscard(r, headersLen)
	return nil
}

func bufferSnippet(b []byte) string {
	n := len(b)
	start := 200
	end := n - start
	if start >= end {
		start = n
		end = n
	}
	bStart, bEnd := b[:start], b[end:]
	if len(bEnd) == 0 {
		return fmt.Sprintf("%q", b)
	}
	return fmt.Sprintf("%q...%q", bStart, bEnd)
}

func isOnlyCRLF(b []byte) bool {
	for _, ch := range b {
		if ch != '\r' && ch != '\n' {
			return false
		}
	}
	return true
}

func init() {
	refreshServerDate()
	go func() {
		for {
			time.Sleep(time.Second)
			refreshServerDate()
		}
	}()
}

var serverDate atomic.Value

func refreshServerDate() {
	b := AppendHTTPDate(nil, time.Now())
	serverDate.Store(b)
}

// Write writes response header to w.
func (h *ResponseHeader) Write(w *bufio.Writer) error {
	_, err := w.Write(h.Header())
	return err
}

// WriteTo writes response header to w.
//
// WriteTo implements io.WriterTo interface.
func (h *ResponseHeader) WriteTo(w io.Writer) (int64, error) {
	n, err := w.Write(h.Header())
	return int64(n), err
}

// Header returns response header representation.
//
// The returned value is valid until the next call to ResponseHeader methods.
func (h *ResponseHeader) Header() []byte {
	h.bufKV.value = h.AppendBytes(h.bufKV.value[:0])
	return h.bufKV.value
}

// String returns response header representation.
func (h *ResponseHeader) String() string {
	return string(h.Header())
}

// AppendBytes appends response header representation to dst and returns
// the extended dst.
func (h *ResponseHeader) AppendBytes(dst []byte) []byte {
	statusCode := h.StatusCode()
	if statusCode < 0 {
		statusCode = StatusOK
	}
	dst = append(dst, statusLine(statusCode)...)

	server := h.Server()
	if len(server) == 0 {
		server = defaultServerName
	}
	dst = appendHeaderLine(dst, strServer, server)
	dst = appendHeaderLine(dst, strDate, serverDate.Load().([]byte))

	// Append Content-Type only for non-zero responses
	// or if it is explicitly set.
	// See https://github.com/valyala/fasthttp/issues/28 .
	if h.ContentLength() != 0 || len(h.contentType) > 0 {
		dst = appendHeaderLine(dst, strContentType, h.ContentType())
	}

	if len(h.contentLengthBytes) > 0 {
		dst = appendHeaderLine(dst, strContentLength, h.contentLengthBytes)
	}

	for i, n := 0, len(h.h); i < n; i++ {
		kv := &h.h[i]
		if !bytes.Equal(kv.key, strDate) {
			dst = appendHeaderLine(dst, kv.key, kv.value)
		}
	}

	n := len(h.cookies)
	if n > 0 {
		for i := 0; i < n; i++ {
			kv := &h.cookies[i]
			dst = appendHeaderLine(dst, strSetCookie, kv.value)
		}
	}

	if h.ConnectionClose() {
		dst = appendHeaderLine(dst, strConnection, strClose)
	}

	return append(dst, strCRLF...)
}

// Write writes request header to w.
func (h *RequestHeader) Write(w *bufio.Writer) error {
	_, err := w.Write(h.Header())
	return err
}

// WriteTo writes request header to w.
//
// WriteTo implements io.WriterTo interface.
func (h *RequestHeader) WriteTo(w io.Writer) (int64, error) {
	n, err := w.Write(h.Header())
	return int64(n), err
}

// Header returns request header representation.
//
// The returned representation is valid until the next call to RequestHeader methods.
func (h *RequestHeader) Header() []byte {
	h.bufKV.value = h.AppendBytes(h.bufKV.value[:0])
	return h.bufKV.value
}

// String returns request header representation.
func (h *RequestHeader) String() string {
	return string(h.Header())
}

// AppendBytes appends request header representation to dst and returns
// the extended dst.
func (h *RequestHeader) AppendBytes(dst []byte) []byte {
	// there is no need in h.parseRawHeaders() here - raw headers are specially handled below.
	dst = append(dst, h.Method()...)
	dst = append(dst, ' ')
	dst = append(dst, h.RequestURI()...)
	dst = append(dst, ' ')
	dst = append(dst, strHTTP11...)
	dst = append(dst, strCRLF...)

	if !h.rawHeadersParsed && len(h.rawHeaders) > 0 {
		return append(dst, h.rawHeaders...)
	}

	userAgent := h.UserAgent()
	if len(userAgent) == 0 {
		userAgent = defaultUserAgent
	}
	dst = appendHeaderLine(dst, strUserAgent, userAgent)

	host := h.Host()
	if len(host) > 0 {
		dst = appendHeaderLine(dst, strHost, host)
	}

	contentType := h.ContentType()
	if !h.noBody() {
		if len(contentType) == 0 {
			contentType = strPostArgsContentType
		}
		dst = appendHeaderLine(dst, strContentType, contentType)

		if len(h.contentLengthBytes) > 0 {
			dst = appendHeaderLine(dst, strContentLength, h.contentLengthBytes)
		}
	} else if len(contentType) > 0 {
		dst = appendHeaderLine(dst, strContentType, contentType)
	}

	for i, n := 0, len(h.h); i < n; i++ {
		kv := &h.h[i]
		dst = appendHeaderLine(dst, kv.key, kv.value)
	}

	// there is no need in h.collectCookies() here, since if cookies aren't collected yet,
	// they all are located in h.h.
	n := len(h.cookies)
	if n > 0 {
		dst = append(dst, strCookie...)
		dst = append(dst, strColonSpace...)
		dst = appendRequestCookieBytes(dst, h.cookies)
		dst = append(dst, strCRLF...)
	}

	if h.ConnectionClose() {
		dst = appendHeaderLine(dst, strConnection, strClose)
	}

	return append(dst, strCRLF...)
}

func appendHeaderLine(dst, key, value []byte) []byte {
	dst = append(dst, key...)
	dst = append(dst, strColonSpace...)
	dst = append(dst, value...)
	return append(dst, strCRLF...)
}

func (h *ResponseHeader) parse(buf []byte) (int, error) {
	m, err := h.parseFirstLine(buf)
	if err != nil {
		return 0, err
	}
	n, err := h.parseHeaders(buf[m:])
	if err != nil {
		return 0, err
	}
	return m + n, nil
}

func (h *RequestHeader) noBody() bool {
	return h.IsGet() || h.IsHead()
}

func (h *RequestHeader) parse(buf []byte) (int, error) {
	m, err := h.parseFirstLine(buf)
	if err != nil {
		return 0, err
	}

	var n int
	if !h.noBody() || h.noHTTP11 {
		n, err = h.parseHeaders(buf[m:])
		if err != nil {
			return 0, err
		}
		h.rawHeadersParsed = true
	} else {
		var rawHeaders []byte
		rawHeaders, n, err = readRawHeaders(h.rawHeaders[:0], buf[m:])
		if err != nil {
			return 0, err
		}
		h.rawHeaders = rawHeaders
	}
	return m + n, nil
}

func (h *ResponseHeader) parseFirstLine(buf []byte) (int, error) {
	bNext := buf
	var b []byte
	var err error
	for len(b) == 0 {
		if b, bNext, err = nextLine(bNext); err != nil {
			return 0, err
		}
	}

	// parse protocol
	n := bytes.IndexByte(b, ' ')
	if n < 0 {
		return 0, fmt.Errorf("cannot find whitespace in the first line of response %q", buf)
	}
	h.noHTTP11 = !bytes.Equal(b[:n], strHTTP11)
	b = b[n+1:]

	// parse status code
	h.statusCode, n, err = parseUintBuf(b)
	if err != nil {
		return 0, fmt.Errorf("cannot parse response status code: %s. Response %q", err, buf)
	}
	if len(b) > n && b[n] != ' ' {
		return 0, fmt.Errorf("unexpected char at the end of status code. Response %q", buf)
	}

	return len(buf) - len(bNext), nil
}

func (h *RequestHeader) parseFirstLine(buf []byte) (int, error) {
	bNext := buf
	var b []byte
	var err error
	for len(b) == 0 {
		if b, bNext, err = nextLine(bNext); err != nil {
			return 0, err
		}
	}

	// parse method
	n := bytes.IndexByte(b, ' ')
	if n <= 0 {
		return 0, fmt.Errorf("cannot find http request method in %q", buf)
	}
	h.method = append(h.method[:0], b[:n]...)
	b = b[n+1:]

	// parse requestURI
	n = bytes.LastIndexByte(b, ' ')
	if n < 0 {
		h.noHTTP11 = true
		n = len(b)
	} else if n == 0 {
		return 0, fmt.Errorf("requestURI cannot be empty in %q", buf)
	} else if !bytes.Equal(b[n+1:], strHTTP11) {
		h.noHTTP11 = true
	}
	h.requestURI = append(h.requestURI[:0], b[:n]...)

	return len(buf) - len(bNext), nil
}

func peekRawHeader(buf, key []byte) []byte {
	n := bytes.Index(buf, key)
	if n < 0 {
		return nil
	}
	if n > 0 && buf[n-1] != '\n' {
		return nil
	}
	n += len(key)
	if n >= len(buf) {
		return nil
	}
	if buf[n] != ':' {
		return nil
	}
	n++
	if buf[n] != ' ' {
		return nil
	}
	n++
	buf = buf[n:]
	n = bytes.IndexByte(buf, '\n')
	if n < 0 {
		return nil
	}
	if n > 0 && buf[n-1] == '\r' {
		n--
	}
	return buf[:n]
}

func readRawHeaders(dst, buf []byte) ([]byte, int, error) {
	n := bytes.IndexByte(buf, '\n')
	if n < 0 {
		return nil, 0, errNeedMore
	}
	if (n == 1 && buf[0] == '\r') || n == 0 {
		// empty headers
		return dst, n + 1, nil
	}

	n++
	b := buf
	m := n
	for {
		b = b[m:]
		m = bytes.IndexByte(b, '\n')
		if m < 0 {
			return nil, 0, errNeedMore
		}
		m++
		n += m
		if (m == 2 && b[0] == '\r') || m == 1 {
			dst = append(dst, buf[:n]...)
			return dst, n, nil
		}
	}
}

func (h *ResponseHeader) parseHeaders(buf []byte) (int, error) {
	// 'identity' content-length by default
	h.contentLength = -2

	var s headerScanner
	s.b = buf
	s.disableNormalizing = h.disableNormalizing
	var err error
	var kv *argsKV
	for s.next() {
		switch string(s.key) {
		case "Content-Type":
			h.contentType = append(h.contentType[:0], s.value...)
		case "Server":
			h.server = append(h.server[:0], s.value...)
		case "Content-Length":
			if h.contentLength != -1 {
				if h.contentLength, err = parseContentLength(s.value); err != nil {
					h.contentLength = -2
				} else {
					h.contentLengthBytes = append(h.contentLengthBytes[:0], s.value...)
				}
			}
		case "Transfer-Encoding":
			if !bytes.Equal(s.value, strIdentity) {
				h.contentLength = -1
				h.h = setArgBytes(h.h, strTransferEncoding, strChunked)
			}
		case "Set-Cookie":
			h.cookies, kv = allocArg(h.cookies)
			kv.key = getCookieKey(kv.key, s.value)
			kv.value = append(kv.value[:0], s.value...)
		case "Connection":
			if bytes.Equal(s.value, strClose) {
				h.connectionClose = true
			} else {
				h.connectionClose = false
				h.h = appendArgBytes(h.h, s.key, s.value)
			}
		default:
			h.h = appendArgBytes(h.h, s.key, s.value)
		}
	}
	if s.err != nil {
		h.connectionClose = true
		return 0, s.err
	}

	if h.contentLength < 0 {
		h.contentLengthBytes = h.contentLengthBytes[:0]
	}
	if h.contentLength == -2 && !h.ConnectionUpgrade() && !h.mustSkipContentLength() {
		h.h = setArgBytes(h.h, strTransferEncoding, strIdentity)
		h.connectionClose = true
	}
	if h.noHTTP11 && !h.connectionClose {
		// close connection for non-http/1.1 response unless 'Connection: keep-alive' is set.
		v := peekArgBytes(h.h, strConnection)
		h.connectionClose = !hasHeaderValue(v, strKeepAlive) && !hasHeaderValue(v, strKeepAliveCamelCase)
	}

	return len(buf) - len(s.b), nil
}

func (h *RequestHeader) parseHeaders(buf []byte) (int, error) {
	h.contentLength = -2

	var s headerScanner
	s.b = buf
	s.disableNormalizing = h.disableNormalizing
	var err error
	for s.next() {
		switch string(s.key) {
		case "Host":
			h.host = append(h.host[:0], s.value...)
		case "User-Agent":
			h.userAgent = append(h.userAgent[:0], s.value...)
		case "Content-Type":
			h.contentType = append(h.contentType[:0], s.value...)
		case "Content-Length":
			if h.contentLength != -1 {
				if h.contentLength, err = parseContentLength(s.value); err != nil {
					h.contentLength = -2
				} else {
					h.contentLengthBytes = append(h.contentLengthBytes[:0], s.value...)
				}
			}
		case "Transfer-Encoding":
			if !bytes.Equal(s.value, strIdentity) {
				h.contentLength = -1
				h.h = setArgBytes(h.h, strTransferEncoding, strChunked)
			}
		case "Connection":
			if bytes.Equal(s.value, strClose) {
				h.connectionClose = true
			} else {
				h.connectionClose = false
				h.h = appendArgBytes(h.h, s.key, s.value)
			}
		default:
			h.h = appendArgBytes(h.h, s.key, s.value)
		}
	}
	if s.err != nil {
		h.connectionClose = true
		return 0, s.err
	}

	if h.contentLength < 0 {
		h.contentLengthBytes = h.contentLengthBytes[:0]
	}
	if h.noBody() {
		h.contentLength = 0
		h.contentLengthBytes = h.contentLengthBytes[:0]
	}
	if h.noHTTP11 && !h.connectionClose {
		// close connection for non-http/1.1 request unless 'Connection: keep-alive' is set.
		v := peekArgBytes(h.h, strConnection)
		h.connectionClose = !hasHeaderValue(v, strKeepAlive) && !hasHeaderValue(v, strKeepAliveCamelCase)
	}

	return len(buf) - len(s.b), nil
}

func (h *RequestHeader) parseRawHeaders() {
	if h.rawHeadersParsed {
		return
	}
	h.rawHeadersParsed = true
	if len(h.rawHeaders) == 0 {
		return
	}
	h.parseHeaders(h.rawHeaders)
}

func (h *RequestHeader) collectCookies() {
	if h.cookiesCollected {
		return
	}

	for i, n := 0, len(h.h); i < n; i++ {
		kv := &h.h[i]
		if bytes.Equal(kv.key, strCookie) {
			h.cookies = parseRequestCookies(h.cookies, kv.value)
			tmp := *kv
			copy(h.h[i:], h.h[i+1:])
			n--
			i--
			h.h[n] = tmp
			h.h = h.h[:n]
		}
	}
	h.cookiesCollected = true
}

func parseContentLength(b []byte) (int, error) {
	v, n, err := parseUintBuf(b)
	if err != nil {
		return -1, err
	}
	if n != len(b) {
		return -1, fmt.Errorf("non-numeric chars at the end of Content-Length")
	}
	return v, nil
}

type headerScanner struct {
	b     []byte
	key   []byte
	value []byte
	err   error

	disableNormalizing bool
}

func (s *headerScanner) next() bool {
	bLen := len(s.b)
	if bLen >= 2 && s.b[0] == '\r' && s.b[1] == '\n' {
		s.b = s.b[2:]
		return false
	}
	if bLen >= 1 && s.b[0] == '\n' {
		s.b = s.b[1:]
		return false
	}
	n := bytes.IndexByte(s.b, ':')
	if n < 0 {
		s.err = errNeedMore
		return false
	}
	s.key = s.b[:n]
	normalizeHeaderKey(s.key, s.disableNormalizing)
	n++
	for len(s.b) > n && s.b[n] == ' ' {
		n++
	}
	s.b = s.b[n:]
	n = bytes.IndexByte(s.b, '\n')
	if n < 0 {
		s.err = errNeedMore
		return false
	}
	s.value = s.b[:n]
	s.b = s.b[n+1:]

	if n > 0 && s.value[n-1] == '\r' {
		n--
	}
	for n > 0 && s.value[n-1] == ' ' {
		n--
	}
	s.value = s.value[:n]
	return true
}

type headerValueScanner struct {
	b     []byte
	value []byte
}

func (s *headerValueScanner) next() bool {
	b := s.b
	if len(b) == 0 {
		return false
	}
	n := bytes.IndexByte(b, ',')
	if n < 0 {
		s.value = stripSpace(b)
		s.b = b[len(b):]
		return true
	}
	s.value = stripSpace(b[:n])
	s.b = b[n+1:]
	return true
}

func stripSpace(b []byte) []byte {
	for len(b) > 0 && b[0] == ' ' {
		b = b[1:]
	}
	for len(b) > 0 && b[len(b)-1] == ' ' {
		b = b[:len(b)-1]
	}
	return b
}

func hasHeaderValue(s, value []byte) bool {
	var vs headerValueScanner
	vs.b = s
	for vs.next() {
		if bytes.Equal(vs.value, value) {
			return true
		}
	}
	return false
}

func nextLine(b []byte) ([]byte, []byte, error) {
	nNext := bytes.IndexByte(b, '\n')
	if nNext < 0 {
		return nil, nil, errNeedMore
	}
	n := nNext
	if n > 0 && b[n-1] == '\r' {
		n--
	}
	return b[:n], b[nNext+1:], nil
}

func initHeaderKV(kv *argsKV, key, value string, disableNormalizing bool) {
	kv.key = getHeaderKeyBytes(kv, key, disableNormalizing)
	kv.value = append(kv.value[:0], value...)
}

func getHeaderKeyBytes(kv *argsKV, key string, disableNormalizing bool) []byte {
	kv.key = append(kv.key[:0], key...)
	normalizeHeaderKey(kv.key, disableNormalizing)
	return kv.key
}

func normalizeHeaderKey(b []byte, disableNormalizing bool) {
	if disableNormalizing {
		return
	}

	n := len(b)
	if n == 0 {
		return
	}

	b[0] = toUpperTable[b[0]]
	for i := 1; i < n; i++ {
		p := &b[i]
		if *p == '-' {
			i++
			if i < n {
				b[i] = toUpperTable[b[i]]
			}
			continue
		}
		*p = toLowerTable[*p]
	}
}

// AppendNormalizedHeaderKey appends normalized header key (name) to dst
// and returns the resulting dst.
//
// Normalized header key starts with uppercase letter. The first letters
// after dashes are also uppercased. All the other letters are lowercased.
// Examples:
//
//   * coNTENT-TYPe -> Content-Type
//   * HOST -> Host
//   * foo-bar-baz -> Foo-Bar-Baz
func AppendNormalizedHeaderKey(dst []byte, key string) []byte {
	dst = append(dst, key...)
	normalizeHeaderKey(dst[len(dst)-len(key):], false)
	return dst
}

// AppendNormalizedHeaderKeyBytes appends normalized header key (name) to dst
// and returns the resulting dst.
//
// Normalized header key starts with uppercase letter. The first letters
// after dashes are also uppercased. All the other letters are lowercased.
// Examples:
//
//   * coNTENT-TYPe -> Content-Type
//   * HOST -> Host
//   * foo-bar-baz -> Foo-Bar-Baz
func AppendNormalizedHeaderKeyBytes(dst, key []byte) []byte {
	return AppendNormalizedHeaderKey(dst, b2s(key))
}

var (
	errNeedMore    = errors.New("need more data: cannot find trailing lf")
	errSmallBuffer = errors.New("small read buffer. Increase ReadBufferSize")
)

// ErrSmallBuffer is returned when the provided buffer size is too small
// for reading request and/or response headers.
//
// ReadBufferSize value from Server or clients should reduce the number
// of such errors.
type ErrSmallBuffer struct {
	error
}

func mustPeekBuffered(r *bufio.Reader) []byte {
	buf, err := r.Peek(r.Buffered())
	if len(buf) == 0 || err != nil {
		panic(fmt.Sprintf("bufio.Reader.Peek() returned unexpected data (%q, %v)", buf, err))
	}
	return buf
}

func mustDiscard(r *bufio.Reader, n int) {
	if _, err := r.Discard(n); err != nil {
		panic(fmt.Sprintf("bufio.Reader.Discard(%d) failed: %s", n, err))
	}
}
