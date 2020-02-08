package nethttpadaptor

import (
	"io/ioutil"
	"net"
	"net/http"

	log "github.com/sirupsen/logrus"
	"github.com/valyala/fasthttp"
)

// NewNetHTTPHandlerFunc wraps a fasthttp.RequestHandler in a http.HandlerFunc
func NewNetHTTPHandlerFunc(h fasthttp.RequestHandler) http.HandlerFunc {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := fasthttp.RequestCtx{}
		remoteIP := net.ParseIP(r.RemoteAddr)
		remoteAddr := net.IPAddr{remoteIP, ""} //nolint
		c.Init(&fasthttp.Request{}, &remoteAddr, nil)

		reqBody, err := ioutil.ReadAll(r.Body)
		if err != nil {
			log.Errorf("error reading request body, %+v", err)
			return
		}
		c.Request.SetBody(reqBody)
		c.Request.SetRequestURI(r.URL.RequestURI())
		c.Request.URI().SetScheme(r.URL.Scheme)
		c.Request.SetHost(r.Host)
		c.Request.Header.SetMethod(r.Method)
		c.Request.Header.Set("Proto", r.Proto)
		c.Request.Header.Set("ProtoMajor", string(r.ProtoMajor))
		c.Request.Header.Set("ProtoMinor", string(r.ProtoMinor))
		c.Request.Header.SetContentType(r.Header.Get("Content-Type"))
		c.Request.Header.SetContentLength(int(r.ContentLength))
		c.Request.Header.SetReferer(r.Referer())
		c.Request.Header.SetUserAgent(r.UserAgent())
		for _, cookie := range r.Cookies() {
			c.Request.Header.SetCookie(cookie.Name, cookie.Value)
		}
		for k, v := range r.Header {
			for _, i := range v {
				c.Request.Header.Add(k, i)
			}
		}

		h(&c)

		c.Response.Header.VisitAll(func(k []byte, v []byte) {
			w.Header().Add(string(k), string(v))
		})
		c.Response.BodyWriteTo(w)
	})
}
