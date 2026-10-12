/*
Copyright 2021 The Dapr Authors
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

package http

import (
	"net/http"
	"net/url"
	"path"
	"strings"

	chi "github.com/go-chi/chi/v5"

	securityConsts "github.com/dapr/dapr/pkg/security/consts"
	"github.com/dapr/kit/streams"
)

// MaxBodySizeMiddleware limits the body size to the given size (in bytes).
func MaxBodySizeMiddleware(maxSize int64) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = streams.LimitReadCloser(r.Body, maxSize)
			next.ServeHTTP(w, r)
		})
	}
}

// APITokenAuthMiddleware enforces authentication using the dapr-api-token header.
func APITokenAuthMiddleware(token string) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if token == "" {
			return next
		}

		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			v := r.Header.Get(securityConsts.APITokenHeader)
			if v != token && !isRouteExcludedFromAPITokenAuth(r.Method, r.URL) {
				http.Error(w, "invalid api token", http.StatusUnauthorized)
				return
			}

			r.Header.Del(securityConsts.APITokenHeader)
			next.ServeHTTP(w, r)
		})
	}
}

func isRouteExcludedFromAPITokenAuth(method string, u *url.URL) bool {
	path := strings.Trim(u.Path, "/")
	switch path {
	case apiVersionV1 + "/healthz":
		return method == http.MethodGet
	case apiVersionV1 + "/healthz/outbound":
		return method == http.MethodGet
	default:
		return false
	}
}

// StripSlashesMiddleware is a middleware that will match request paths with a trailing
// slash, strip it from the path and continue routing through the mux, if a route
// matches, then it will serve the handler.
//
// This is a modified version of the code from https://github.com/go-chi/chi/blob/v5.0.8/middleware/strip.go
// It does not remove the trailing slash if it matches a route already.
// Original code Copyright (c) 2015-present Peter Kieltyka (https://github.com/pkieltyka), Google Inc.
// Original code license: MIT: https://github.com/go-chi/chi/blob/v5.0.8/LICENSE
func StripSlashesMiddleware(next http.Handler) http.Handler {
	fn := func(w http.ResponseWriter, r *http.Request) {
		var path string
		rctx := chi.RouteContext(r.Context())
		if rctx != nil && rctx.RoutePath != "" {
			path = rctx.RoutePath
		} else {
			path = r.URL.EscapedPath()
		}
		if len(path) > 1 && path[len(path)-1] == '/' {
			newPath := path[:len(path)-1]
			// Replace only if the route doesn't already have a match or if the context was not a chi.RouteContext
			if rctx == nil {
				r.URL.Path = newPath
			} else if !rctx.Routes.Match(rctx, r.Method, path) {
				rctx.RoutePath = newPath
			}
		}
		next.ServeHTTP(w, r)
	}
	return http.HandlerFunc(fn)
}

// CleanPathMiddleware middleware will clean out double slash mistakes from a user's request path.
// For example, if a user requests /users//1 or //users////1 will both be treated as: /users/1
//
// This is a modified version of the code from https://github.com/go-chi/chi/blob/v5.0.8/middleware/clean_path.go
// Original code Copyright (c) 2015-present Peter Kieltyka (https://github.com/pkieltyka), Google Inc.
// Original code license: MIT: https://github.com/go-chi/chi/blob/v5.0.8/LICENSE
func CleanPathMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rctx := chi.RouteContext(r.Context())

		// Set RoutePath to the EscapedPath always
		// See: https://github.com/go-chi/chi/issues/641
		rctx.RoutePath = path.Clean(r.URL.EscapedPath())

		next.ServeHTTP(w, r)
	})
}
