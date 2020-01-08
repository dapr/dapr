// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package oauth2

import (
	"encoding/json"
	"strings"

	"context"

	"github.com/dapr/components-contrib/middleware"
	"github.com/fasthttp-contrib/sessions"
	"github.com/google/uuid"
	"github.com/valyala/fasthttp"
	"golang.org/x/oauth2"
)

// Metadata is the oAuth middleware config
type oAuth2MiddlewareMetadata struct {
	ClientID       string `json:"clientID"`
	ClientSecret   string `json:"clientSecret"`
	Scopes         string `json:"scopes"`
	AuthURL        string `json:"authURL"`
	TokenURL       string `json:"tokenURL"`
	AuthHeaderName string `json:"authHeaderName"`
	RedirectURL    string `json:"redirectURL"`
}

// NewOAuth2Middleware returns a new oAuth2 middleware
func NewOAuth2Middleware() *Middleware {
	return &Middleware{}
}

// Middleware is an oAuth2 authentication middleware
type Middleware struct {
}

const (
	stateParam   = "state"
	savedState   = "auth-state"
	redirectPath = "redirect-url"
	codeParam    = "code"
)

// GetHandler retruns the HTTP handler provided by the middleware
func (m *Middleware) GetHandler(metadata middleware.Metadata) (func(h fasthttp.RequestHandler) fasthttp.RequestHandler, error) {
	meta, err := m.getNativeMetadata(metadata)
	if err != nil {
		return nil, err
	}

	return func(h fasthttp.RequestHandler) fasthttp.RequestHandler {
		return func(ctx *fasthttp.RequestCtx) {
			conf := &oauth2.Config{
				ClientID:     meta.ClientID,
				ClientSecret: meta.ClientSecret,
				Scopes:       strings.Split(meta.Scopes, ","),
				RedirectURL:  meta.RedirectURL,
				Endpoint: oauth2.Endpoint{
					AuthURL:  meta.AuthURL,
					TokenURL: meta.TokenURL,
				},
			}
			session := sessions.StartFasthttp(ctx)
			if session.GetString(meta.AuthHeaderName) != "" {
				ctx.Request.Header.Add(meta.AuthHeaderName, session.GetString(meta.AuthHeaderName))
				h(ctx)
				return
			}
			state := string(ctx.FormValue(stateParam))
			if state == "" {
				id, _ := uuid.NewUUID()
				session.Set(savedState, id.String())
				session.Set(redirectPath, string(ctx.RequestURI()))
				url := conf.AuthCodeURL(id.String(), oauth2.AccessTypeOffline)
				ctx.Redirect(url, 302)
			} else {
				authState := session.GetString(savedState)
				redirectURL := session.GetString(redirectPath)
				if state != authState {
					ctx.Error("invalid state", fasthttp.StatusBadRequest)
				} else {
					code := string(ctx.FormValue(codeParam))
					if code == "" {
						ctx.Error("code not found", fasthttp.StatusBadRequest)
					} else {
						token, err := conf.Exchange(context.Background(), code)
						if err != nil {
							ctx.Error(err.Error(), fasthttp.StatusInternalServerError)
						}
						session.Set(meta.AuthHeaderName, token.Type()+" "+token.AccessToken)
						ctx.Request.Header.Add(meta.AuthHeaderName, token.Type()+" "+token.AccessToken)
						ctx.Redirect(redirectURL, 302)
					}
				}
			}
		}
	}, nil
}

func (m *Middleware) getNativeMetadata(metadata middleware.Metadata) (*oAuth2MiddlewareMetadata, error) {
	b, err := json.Marshal(metadata.Properties)
	if err != nil {
		return nil, err
	}

	var middlewareMetadata oAuth2MiddlewareMetadata
	err = json.Unmarshal(b, &middlewareMetadata)
	if err != nil {
		return nil, err
	}
	return &middlewareMetadata, nil
}
