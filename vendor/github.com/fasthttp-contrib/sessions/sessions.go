// Package sessions provides sessions support for net/http
// unique with auto-GC, register unlimited number of databases to Load and Update/Save the sessions in external server or to an external (no/or/and sql) database
// Usage net/http:
// // init a new sessions manager( if you use only one web framework inside your app then you can use the package-level functions like: sessions.Start/sessions.Destroy)
// manager := sessions.New(sessions.Config{})
// // start a session for a particular client
// manager.Start(http.ResponseWriter, *http.Request)
//
// // destroy a session from the server and client,
//  // don't call it on each handler, only on the handler you want the client to 'logout' or something like this:
// manager.Destroy(http.ResponseWriter, *http.Request)
//
//
// Usage valyala/fasthttp:
// // init a new sessions manager( if you use only one web framework inside your app then you can use the package-level functions like: sessions.Start/sessions.Destroy)
// manager := sessions.New(sessions.Config{})
// // start a session for a particular client
// manager.StartFasthttp(*fasthttp.RequestCtx)
//
// // destroy a session from the server and client,
//  // don't call it on each handler, only on the handler you want the client to 'logout' or something like this:
// manager.DestroyFasthttp(*fasthttp.Request)
//
// Note that, now, you can use both fasthttp and net/http within the same sessions manager(.New) instance!
// So now, you can share sessions between a net/http app and valyala/fasthttp app
package sessions

import (
	"encoding/base64"
	"github.com/valyala/fasthttp"
	"net/http"
	"strings"
	"time"
)

const (
	// Version current version number
	Version = "0.0.3"
)

type (
	// Sessions is the start point of this package
	// contains all the registered sessions and manages them
	Sessions interface {
		// UpdateConfig updates the configuration field (Config does not receives a pointer, so this is a way to update a pre-defined configuration)
		UpdateConfig(Config)
		// UseDatabase ,optionally, adds a session database to the manager's provider,
		// a session db doesn't have write access
		// see https://github.com/kataras/go-sessions/tree/master/sessiondb
		UseDatabase(Database)
		// Start starts the session for the particular net/http request
		Start(http.ResponseWriter, *http.Request) Session
		// Destroy kills the net/http session and remove the associated cookie
		Destroy(http.ResponseWriter, *http.Request)
		// Start starts the session for the particular valyala/fasthttp request
		StartFasthttp(*fasthttp.RequestCtx) Session
		// Destroy kills the valyala/fasthttp session and remove the associated cookie
		DestroyFasthttp(*fasthttp.RequestCtx)
	}
	// sessions contains the cookie's name, the provider and a duration for GC and cookie life expire
	sessions struct {
		config   Config
		provider *Provider
	}
)

// New creates & returns a new Sessions(manager) and start its GC
func New(c Config) Sessions {
	c = c.Validate()
	// init and start the sess manager
	sess := &sessions{config: c, provider: NewProvider(c.Expires)}
	//run the GC here
	go sess.gc()
	return sess
}

var defaultSessions = New(Config{
	Cookie:                      DefaultCookieName,
	DecodeCookie:                false,
	Expires:                     DefaultCookieExpires,
	CookieLength:                DefaultCookieLength,
	GcDuration:                  DefaultGcDuration,
	DisableSubdomainPersistence: false,
})

// UpdateConfig updates the sessions configuration
func UpdateConfig(c Config) {
	defaultSessions.UpdateConfig(c)
}

// UpdateConfig updates the sessions configuration
func (s *sessions) UpdateConfig(c Config) {
	s.config = c.Validate()
}

// UseDatabase adds a session database to the manager's provider,
// a session db doesn't have write access
func UseDatabase(db Database) {
	defaultSessions.UseDatabase(db)
}

// UseDatabase adds a session database to the manager's provider,
// a session db doesn't have write access
func (s *sessions) UseDatabase(db Database) {
	s.provider.Expires = s.config.Expires // updae the expires confiuration field for any case
	s.provider.RegisterDatabase(db)
}

// Start starts the session for the particular net/http request
func Start(res http.ResponseWriter, req *http.Request) Session {
	return defaultSessions.Start(res, req)
}

// Start starts the session for the particular net/http request
func (s *sessions) Start(res http.ResponseWriter, req *http.Request) Session {
	var sess Session

	cookieValue := GetCookie(s.config.Cookie, req)

	if cookieValue == "" { // cookie doesn't exists, let's generate a session and add set a cookie
		sid := GenerateSessionID(s.config.CookieLength)
		sess = s.provider.Init(sid)
		//cookie := &http.Cookie{}
		cookie := AcquireCookie()
		// The RFC makes no mention of encoding url value, so here I think to encode both sessionid key and the value using the safe(to put and to use as cookie) url-encoding
		cookie.Name = s.config.Cookie
		cookie.Value = sid
		cookie.Path = "/"
		if !s.config.DisableSubdomainPersistence {

			requestDomain := req.Host
			if portIdx := strings.IndexByte(requestDomain, ':'); portIdx > 0 {
				requestDomain = requestDomain[0:portIdx]
			}
			if IsValidCookieDomain(requestDomain) {

				// RFC2109, we allow level 1 subdomains, but no further
				// if we have localhost.com , we want the localhost.cos.
				// so if we have something like: mysubdomain.localhost.com we want the localhost here
				// if we have mysubsubdomain.mysubdomain.localhost.com we want the .mysubdomain.localhost.com here
				// slow things here, especially the 'replace' but this is a good and understable( I hope) way to get the be able to set cookies from subdomains & domain with 1-level limit
				if dotIdx := strings.LastIndexByte(requestDomain, '.'); dotIdx > 0 {
					// is mysubdomain.localhost.com || mysubsubdomain.mysubdomain.localhost.com
					s := requestDomain[0:dotIdx] // set mysubdomain.localhost || mysubsubdomain.mysubdomain.localhost
					if secondDotIdx := strings.LastIndexByte(s, '.'); secondDotIdx > 0 {
						//is mysubdomain.localhost ||  mysubsubdomain.mysubdomain.localhost
						s = s[secondDotIdx+1:] // set to localhost || mysubdomain.localhost
					}
					// replace the s with the requestDomain before the domain's siffux
					subdomainSuff := strings.LastIndexByte(requestDomain, '.')
					if subdomainSuff > len(s) { // if it is actual exists as subdomain suffix
						requestDomain = strings.Replace(requestDomain, requestDomain[0:subdomainSuff], s, 1) // set to localhost.com || mysubdomain.localhost.com
					}
				}
				// finally set the .localhost.com (for(1-level) || .mysubdomain.localhost.com (for 2-level subdomain allow)
				cookie.Domain = "." + requestDomain // . to allow persistance
			}

		}
		cookie.HttpOnly = true
		if s.config.Expires == 0 {
			// unlimited life
			cookie.Expires = CookieExpireUnlimited
		} else if s.config.Expires > 0 {
			cookie.Expires = time.Now().Add(s.config.Expires)
		} // if it's -1 then the cookie is deleted when the browser closes

		AddCookie(cookie, res)
		ReleaseCookie(cookie)
	} else {
		sess = s.provider.Read(cookieValue)
	}
	return sess
}

// Destroy kills the net/http session and remove the associated cookie
func Destroy(res http.ResponseWriter, req *http.Request) {
	defaultSessions.Destroy(res, req)
}

// Destroy kills the net/http session and remove the associated cookie
func (s *sessions) Destroy(res http.ResponseWriter, req *http.Request) {
	cookieValue := GetCookie(s.config.Cookie, req)
	if cookieValue == "" { // nothing to destroy
		return
	}
	RemoveCookie(s.config.Cookie, res, req)
	s.provider.Destroy(cookieValue)
}

// StartFasthttp starts the session for the particular valyala/fasthttp request
func StartFasthttp(reqCtx *fasthttp.RequestCtx) Session {
	return defaultSessions.StartFasthttp(reqCtx)
}

// Start starts the session for the particular valyala/fasthttp request
func (s *sessions) StartFasthttp(reqCtx *fasthttp.RequestCtx) Session {
	var sess Session

	cookieValue := GetFasthttpCookie(s.config.Cookie, reqCtx)

	if cookieValue == "" { // cookie doesn't exists, let's generate a session and add set a cookie
		sid := GenerateSessionID(s.config.CookieLength)
		sess = s.provider.Init(sid)
		cookie := fasthttp.AcquireCookie()
		//cookie := &fasthttp.Cookie{}
		// The RFC makes no mention of encoding url value, so here I think to encode both sessionid key and the value using the safe(to put and to use as cookie) url-encoding
		cookie.SetKey(s.config.Cookie)
		cookie.SetValue(sid)
		cookie.SetPath("/")
		if !s.config.DisableSubdomainPersistence {
			requestDomain := string(reqCtx.Host())
			if portIdx := strings.IndexByte(requestDomain, ':'); portIdx > 0 {
				requestDomain = requestDomain[0:portIdx]
			}
			if IsValidCookieDomain(requestDomain) {

				// RFC2109, we allow level 1 subdomains, but no further
				// if we have localhost.com , we want the localhost.cos.
				// so if we have something like: mysubdomain.localhost.com we want the localhost here
				// if we have mysubsubdomain.mysubdomain.localhost.com we want the .mysubdomain.localhost.com here
				// slow things here, especially the 'replace' but this is a good and understable( I hope) way to get the be able to set cookies from subdomains & domain with 1-level limit
				if dotIdx := strings.LastIndexByte(requestDomain, '.'); dotIdx > 0 {
					// is mysubdomain.localhost.com || mysubsubdomain.mysubdomain.localhost.com
					s := requestDomain[0:dotIdx] // set mysubdomain.localhost || mysubsubdomain.mysubdomain.localhost
					if secondDotIdx := strings.LastIndexByte(s, '.'); secondDotIdx > 0 {
						//is mysubdomain.localhost ||  mysubsubdomain.mysubdomain.localhost
						s = s[secondDotIdx+1:] // set to localhost || mysubdomain.localhost
					}
					// replace the s with the requestDomain before the domain's siffux
					subdomainSuff := strings.LastIndexByte(requestDomain, '.')
					if subdomainSuff > len(s) { // if it is actual exists as subdomain suffix
						requestDomain = strings.Replace(requestDomain, requestDomain[0:subdomainSuff], s, 1) // set to localhost.com || mysubdomain.localhost.com
					}
				}
				// finally set the .localhost.com (for(1-level) || .mysubdomain.localhost.com (for 2-level subdomain allow)
				cookie.SetDomain("." + requestDomain) // . to allow persistance
			}

		}
		cookie.SetHTTPOnly(true)
		if s.config.Expires == 0 {
			// unlimited life
			cookie.SetExpire(CookieExpireUnlimited)
		} else if s.config.Expires > 0 {
			cookie.SetExpire(time.Now().Add(s.config.Expires))
		} // if it's -1 then the cookie is deleted when the browser closes

		AddFasthttpCookie(cookie, reqCtx)
		fasthttp.ReleaseCookie(cookie)
	} else {
		sess = s.provider.Read(cookieValue)
	}
	return sess
}

// DestroyFasthttp kills the valyala/fasthttp session and remove the associated cookie
func DestroyFasthttp(reqCtx *fasthttp.RequestCtx) {
	defaultSessions.DestroyFasthttp(reqCtx)
}

// DestroyFasthttp kills the valyala/fasthttp session and remove the associated cookie
func (s *sessions) DestroyFasthttp(reqCtx *fasthttp.RequestCtx) {
	cookieValue := GetFasthttpCookie(s.config.Cookie, reqCtx)
	if cookieValue == "" { // nothing to destroy
		return
	}
	RemoveFasthttpCookie(s.config.Cookie, reqCtx)
	s.provider.Destroy(cookieValue)
}

// GC tick-tock for the store cleanup
// it's a blocking function, so run it with go routine, it's totally safe
func (s *sessions) gc() {
	s.provider.GC(s.config.GcDuration)
	// set a timer for the next GC
	time.AfterFunc(s.config.GcDuration, func() {
		s.gc()
	})
}

// GenerateSessionID returns a random string, used to set the session id
func GenerateSessionID(length int) string {
	return base64.URLEncoding.EncodeToString(Random(length))
}
