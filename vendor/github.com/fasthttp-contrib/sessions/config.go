package sessions

import (
	"encoding/base64"
	"time"
)

const (
	// DefaultCookieName the secret cookie's name for sessions
	DefaultCookieName = "gosessionsid"
	// DefaultGcDuration  is the default Session Manager's GCDuration , which is 2 hours
	DefaultGcDuration = time.Duration(2) * time.Hour
	// DefaultCookieExpires  is the default Session Manager's Cookie expire , which is 2 hours
	DefaultCookieExpires = DefaultGcDuration
	// DefaultCookieLength is the default Session Manager's CookieLength, which is 32
	DefaultCookieLength = 32
)

// Config the configuration for sessions
// has 6 fields
// first is the cookieName, the session's name (string) ["mysessionsecretcookieid"]
// second enable if you want to decode the cookie's key also
// third is the time which the client's cookie expires
// forth is the cookie length (sessionid) int, defaults to 32, do not change if you don't have any reason to do
// fifth is the gcDuration (time.Duration) when this time passes it removes the unused sessions from the memory until the user come back
// sixth is the DisableSubdomainPersistence which you can set it to true in order dissallow your q subdomains to have access to the session cook
//
type Config struct {
	// Cookie string, the session's client cookie name, for example: "qsessionid"
	Cookie string
	// DecodeCookie set it to true to decode the cookie key with base64 URLEncoding
	// Defaults to false
	DecodeCookie bool

	// Expires the duration of which the cookie must expires (created_time.Add(Expires)).
	// If you want to delete the cookie when the browser closes, set it to -1 but in this case, the server side's session duration is up to GcDuration
	//
	// Default infinitive/unlimited life duration(0)
	Expires time.Duration

	// CookieLength the length of the sessionid's cookie's value, let it to 0 if you don't want to change it
	// Defaults to 32
	CookieLength int

	// GcDuration every how much duration(GcDuration) the memory should be clear for unused cookies (GcDuration)
	// for example: time.Duration(2)*time.Hour. it will check every 2 hours if cookie hasn't be used for 2 hours,
	// deletes it from backend memory until the user comes back, then the session continue to work as it was
	//
	// Default 2 hours
	GcDuration time.Duration

	// DisableSubdomainPersistence set it to true in order dissallow your q subdomains to have access to the session cookie
	// defaults to false
	DisableSubdomainPersistence bool
}

// Validate corrects missing fields configuration fields and returns the right configuration
func (c Config) Validate() Config {
	if c.Cookie == "" {
		c.Cookie = DefaultCookieName
	}
	if c.GcDuration <= 0 {
		c.GcDuration = DefaultGcDuration
	}
	if c.Expires <= 0 {
		c.Expires = DefaultCookieExpires
	}
	if c.DecodeCookie {
		c.Cookie = base64.URLEncoding.EncodeToString([]byte(c.Cookie)) // change the cookie's name/key to a more safe(?)
		// get the real value for your tests by:
		//sessIdKey := url.QueryEscape(base64.URLEncoding.EncodeToString([]byte(Sessions.Cookie)))
	}

	if c.CookieLength <= 0 {
		c.CookieLength = DefaultCookieLength
	}
	return c
}
