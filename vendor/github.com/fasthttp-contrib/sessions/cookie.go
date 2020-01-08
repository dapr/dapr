package sessions

import (
	"bytes"
	"encoding/base64"
	"encoding/gob"
	"github.com/valyala/fasthttp"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	// CookieExpireDelete may be set on Cookie.Expire for expiring the given cookie.
	CookieExpireDelete = time.Date(2009, time.November, 10, 23, 0, 0, 0, time.UTC)

	// CookieExpireUnlimited indicates that the cookie doesn't expire.
	CookieExpireUnlimited = time.Now().AddDate(23, 0, 0)
)

// GetCookie returns cookie's value by it's name
// returns empty string if nothing was found
func GetCookie(name string, req *http.Request) string {
	c, err := req.Cookie(name)
	if err != nil {
		return ""
	}
	return c.Value
}

// AddCookie adds a cookie
func AddCookie(cookie *http.Cookie, res http.ResponseWriter) {
	if v := cookie.String(); v != "" {
		res.Header().Add("Set-Cookie", v)
	}
}

// RemoveCookie deletes a cookie by it's name/key
func RemoveCookie(name string, res http.ResponseWriter, req *http.Request) {
	c, err := req.Cookie(name)
	if err != nil {
		return
	}

	c.Expires = CookieExpireDelete
	c.MaxAge = -1
	c.Value = ""
	c.Path = "/"
	AddCookie(c, res)
}

var cookiePool sync.Pool

// AcquireCookie returns an empty Cookie object from the pool.
//
// The returned object may be returned back to the pool with ReleaseCookie.
// This allows reducing GC load.
func AcquireCookie() *http.Cookie {
	v := cookiePool.Get()
	if v == nil {
		return &http.Cookie{}
	}

	cookie := v.(*http.Cookie)
	cookie.HttpOnly = true
	cookie.Path = ""
	cookie.HttpOnly = false
	cookie.Name = ""
	cookie.Raw = ""
	cookie.Value = ""
	cookie.Domain = ""
	cookie.MaxAge = -1
	cookie.Expires = CookieExpireUnlimited
	return cookie
}

// ReleaseCookie returns the Cookie object acquired with AcquireCookie back
// to the pool.
//
// Do not access released Cookie object, otherwise data races may occur.
func ReleaseCookie(cookie *http.Cookie) {
	cookiePool.Put(cookie)
}

// FastHTTP

// GetFasthttpCookie returns cookie's value by it's name
// returns empty string if nothing was found
func GetFasthttpCookie(name string, reqCtx *fasthttp.RequestCtx) (val string) {
	bcookie := reqCtx.Request.Header.Cookie(name)
	if bcookie != nil {
		val = string(bcookie)
	}
	return
}

// AddFasthttpCookie adds a cookie to the client
func AddFasthttpCookie(c *fasthttp.Cookie, reqCtx *fasthttp.RequestCtx) {
	reqCtx.Response.Header.SetCookie(c)
}

// RemoveFasthttpCookie deletes a cookie by it's name/key
func RemoveFasthttpCookie(name string, reqCtx *fasthttp.RequestCtx) {
	reqCtx.Response.Header.DelCookie(name)

	cookie := fasthttp.AcquireCookie()
	//cookie := &fasthttp.Cookie{}
	cookie.SetKey(name)
	cookie.SetValue("")
	cookie.SetPath("/")
	cookie.SetHTTPOnly(true)
	exp := time.Now().Add(-time.Duration(1) * time.Minute) //RFC says 1 second, but let's do it 1 minute to make sure is working...
	cookie.SetExpire(exp)
	AddFasthttpCookie(cookie, reqCtx)
	fasthttp.ReleaseCookie(cookie)
	// delete request's cookie also, which is temporarly available
	reqCtx.Request.Header.DelCookie(name)
}

// IsValidCookieDomain returns true if the receiver is a valid domain to set
// valid means that is recognised as 'domain' by the browser, so it(the cookie) can be shared with subdomains also
func IsValidCookieDomain(domain string) bool {
	if domain == "0.0.0.0" || domain == "127.0.0.1" {
		// for these type of hosts, we can't allow subdomains persistance,
		// the web browser doesn't understand the mysubdomain.0.0.0.0 and mysubdomain.127.0.0.1 mysubdomain.32.196.56.181. as scorrectly ubdomains because of the many dots
		// so don't set a cookie domain here, let browser handle this
		return false
	}

	dotLen := strings.Count(domain, ".")
	if dotLen == 0 {
		// we don't have a domain, maybe something like 'localhost', browser doesn't see the .localhost as wildcard subdomain+domain
		return false
	}
	if dotLen >= 3 {
		if lastDotIdx := strings.LastIndexByte(domain, '.'); lastDotIdx != -1 {
			// chekc the last part, if it's number then propably it's ip
			if len(domain) > lastDotIdx+1 {
				_, err := strconv.Atoi(domain[lastDotIdx+1:])
				if err == nil {
					return false
				}
			}
		}
	}

	return true
}

func encodeCookieValue(value string) string {
	return base64.URLEncoding.EncodeToString([]byte(value))
}

func decodeCookieValue(value string) (string, error) {
	v, err := base64.URLEncoding.DecodeString(value)
	if err != nil {
		return "", err
	}
	return string(v), nil
}

// -------------------------------------------------------------------------------------
// -------------------------------------------------------------------------------------
// ----------------------------------Strings & Serialization----------------------------
// -------------------------------------------------------------------------------------
// -------------------------------------------------------------------------------------

const (
	letterBytes   = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	letterIdxBits = 6                    // 6 bits to represent a letter index
	letterIdxMask = 1<<letterIdxBits - 1 // All 1-bits, as many as letterIdxBits
	letterIdxMax  = 63 / letterIdxBits   // # of letter indices fitting in 63 bits
)

var src = rand.NewSource(time.Now().UnixNano())

// Random takes a parameter (int) and returns random slice of byte
// ex: var randomstrbytes []byte; randomstrbytes =  Random(32)
// note: this code doesn't belongs to me, but it works just fine*
func Random(n int) []byte {
	b := make([]byte, n)
	// A src.Int63() generates 63 random bits, enough for letterIdxMax characters!
	for i, cache, remain := n-1, src.Int63(), letterIdxMax; i >= 0; {
		if remain == 0 {
			cache, remain = src.Int63(), letterIdxMax
		}
		if idx := int(cache & letterIdxMask); idx < len(letterBytes) {
			b[i] = letterBytes[idx]
			i--
		}
		cache >>= letterIdxBits
		remain--
	}

	return b
}

// RandomString accepts a number(10 for example) and returns a random string using simple but fairly safe random algorithm
func RandomString(n int) string {
	return string(Random(n))
}

// Serialize serialize any type to gob bytes and after returns its the base64 encoded string
func Serialize(m interface{}) (string, error) {
	b := bytes.Buffer{}
	encoder := gob.NewEncoder(&b)
	err := encoder.Encode(m)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(b.Bytes()), nil
}

// Deserialize accepts an encoded string and a data struct  which will be filled with the desierialized string
// using gob decoder
func Deserialize(str string, m interface{}) error {
	by, err := base64.StdEncoding.DecodeString(str)
	if err != nil {
		return err
	}
	b := bytes.Buffer{}
	b.Write(by)
	d := gob.NewDecoder(&b)
	//	d := gob.NewDecoder(bytes.NewBufferString(str))
	err = d.Decode(&m)
	if err != nil {
		return err
	}
	return nil
}
