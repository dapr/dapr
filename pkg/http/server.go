// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package http

import (
	"fmt"
	"strings"

	cors "github.com/AdhityaRamadhanus/fasthttpcors"
	"github.com/dapr/dapr/pkg/config"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	http_middleware "github.com/dapr/dapr/pkg/middleware/http"
	routing "github.com/qiangxue/fasthttp-routing"
	log "github.com/sirupsen/logrus"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/pprofhandler"
)

// Server is an interface for the Dapr HTTP server
type Server interface {
	StartNonBlocking()
}

type server struct {
	config      ServerConfig
	tracingSpec config.TracingSpec
	pipeline    http_middleware.Pipeline
	api         API
}

// NewServer returns a new HTTP server
func NewServer(api API, config ServerConfig, tracingSpec config.TracingSpec, pipeline http_middleware.Pipeline) Server {
	return &server{
		api:         api,
		config:      config,
		tracingSpec: tracingSpec,
		pipeline:    pipeline,
	}
}

// StartNonBlocking starts a new server in a goroutine
func (s *server) StartNonBlocking() {
	handler :=
		s.useProxy(
			s.useCors(
				s.useComponents(
					s.useRouter())))

	if s.tracingSpec.Enabled {
		handler = s.useTracing(handler)
	}

	go func() {
		log.Fatal(fasthttp.ListenAndServe(fmt.Sprintf(":%v", s.config.Port), handler))
	}()

	if s.config.EnableProfiling {
		go func() {
			log.Infof("starting profiling server on port %v", s.config.ProfilePort)
			log.Fatal(fasthttp.ListenAndServe(fmt.Sprintf(":%v", s.config.ProfilePort), pprofhandler.PprofHandler))
		}()
	}
}

func (s *server) useTracing(next fasthttp.RequestHandler) fasthttp.RequestHandler {
	return diag.TracingHTTPMiddleware(s.tracingSpec, next)
}

func (s *server) useRouter() fasthttp.RequestHandler {
	endpoints := s.api.APIEndpoints()
	router := s.getRouter(endpoints)
	return router.HandleRequest
}

func (s *server) useComponents(next fasthttp.RequestHandler) fasthttp.RequestHandler {
	return s.pipeline.Apply(next)
}

func (s *server) useCors(next fasthttp.RequestHandler) fasthttp.RequestHandler {
	origins := strings.Split(s.config.AllowedOrigins, ",")
	corsHandler := s.getCorsHandler(origins)
	return corsHandler.CorsMiddleware(next)
}

func (s *server) useProxy(next fasthttp.RequestHandler) fasthttp.RequestHandler {
	return func(ctx *fasthttp.RequestCtx) {
		var proto string
		if ctx.IsTLS() {
			proto = "https"
		} else {
			proto = "http"
		}
		// Add Forwarded header: https://tools.ietf.org/html/rfc7239
		ctx.Request.Header.Add("Forwarded",
			fmt.Sprintf("by=%s;for=%s;host=%s;proto=%s",
				ctx.LocalAddr(),
				ctx.RemoteAddr(),
				ctx.Host(),
				proto))
		// Add X-Forwarded-For: https://developer.mozilla.org/en-US/docs/Web/HTTP/Headers/X-Forwarded-For
		ctx.Request.Header.Add("X-Forwarded-For", ctx.RemoteAddr().String())
		// Add X-Forwarded-Host: https://developer.mozilla.org/en-US/docs/Web/HTTP/Headers/X-Forwarded-Host
		ctx.Request.Header.Add("X-Forwarded-Host", fmt.Sprintf("%s", ctx.Host()))
		// Add X-Forwarded-Proto: https://developer.mozilla.org/en-US/docs/Web/HTTP/Headers/X-Forwarded-Proto
		ctx.Request.Header.Add("X-Forwarded-", proto)
		next(ctx)
	}
}

func (s *server) getCorsHandler(allowedOrigins []string) *cors.CorsHandler {
	return cors.NewCorsHandler(cors.Options{
		AllowedOrigins: allowedOrigins,
		Debug:          false,
	})
}

func (s *server) getRouter(endpoints []Endpoint) *routing.Router {
	router := routing.New()

	for _, e := range endpoints {
		methods := strings.Join(e.Methods, ",")
		path := fmt.Sprintf("/%s/%s", e.Version, e.Route)

		router.To(methods, path, e.Handler)
	}

	return router
}
