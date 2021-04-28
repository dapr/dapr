// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package http

import (
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	cors "github.com/AdhityaRamadhanus/fasthttpcors"
	"github.com/dapr/dapr/pkg/config"
	cors_dapr "github.com/dapr/dapr/pkg/cors"
	"github.com/dapr/kit/logger"

	diag "github.com/dapr/dapr/pkg/diagnostics"
	diag_utils "github.com/dapr/dapr/pkg/diagnostics/utils"
	http_middleware "github.com/dapr/dapr/pkg/middleware/http"
	auth "github.com/dapr/dapr/pkg/runtime/security"
	routing "github.com/fasthttp/router"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/pprofhandler"
)

var log = logger.NewLogger("dapr.runtime.http")

// Server is an interface for the Dapr HTTP server
type Server interface {
	StartNonBlocking()
}

type server struct {
	config      ServerConfig
	tracingSpec config.TracingSpec
	metricSpec  config.MetricSpec
	pipeline    http_middleware.Pipeline
	api         API
}

// NewServer returns a new HTTP server
func NewServer(api API, config ServerConfig, tracingSpec config.TracingSpec, metricSpec config.MetricSpec, pipeline http_middleware.Pipeline) Server {
	return &server{
		api:         api,
		config:      config,
		tracingSpec: tracingSpec,
		metricSpec:  metricSpec,
		pipeline:    pipeline,
	}
}

// StartNonBlocking starts a new server in a goroutine
func (s *server) StartNonBlocking() {
	handler :=
		useAPIAuthentication(
			s.useCors(
				s.useComponents(
					s.useRouter())))

	handler = s.useMetrics(handler)
	handler = s.useTracing(handler)

	customServer := &fasthttp.Server{
		Handler:            handler,
		MaxRequestBodySize: s.config.MaxRequestBodySize * 1024 * 1024,
	}

	go func() {
		log.Fatal(customServer.ListenAndServe(fmt.Sprintf(":%v", s.config.Port)))
	}()

	if s.config.EnableProfiling {
		go func() {
			log.Infof("starting profiling server on port %v", s.config.ProfilePort)
			log.Fatal(fasthttp.ListenAndServe(fmt.Sprintf(":%v", s.config.ProfilePort), pprofhandler.PprofHandler))
		}()
	}
}

func (s *server) useTracing(next fasthttp.RequestHandler) fasthttp.RequestHandler {
	if diag_utils.IsTracingEnabled(s.tracingSpec.SamplingRate) {
		log.Infof("enabled tracing http middleware")
		return diag.HTTPTraceMiddleware(next, s.config.AppID, s.tracingSpec)
	}
	return next
}

func (s *server) useMetrics(next fasthttp.RequestHandler) fasthttp.RequestHandler {
	if s.metricSpec.Enabled {
		log.Infof("enabled metrics http middleware")
		return diag.DefaultHTTPMonitoring.FastHTTPMiddleware(next)
	}
	return next
}

func (s *server) useRouter() fasthttp.RequestHandler {
	endpoints := s.api.APIEndpoints()
	router := s.getRouter(endpoints)
	return router.Handler
}

func (s *server) useComponents(next fasthttp.RequestHandler) fasthttp.RequestHandler {
	return s.pipeline.Apply(next)
}

func (s *server) useCors(next fasthttp.RequestHandler) fasthttp.RequestHandler {
	if s.config.AllowedOrigins == cors_dapr.DefaultAllowedOrigins {
		return next
	}

	log.Infof("enabled cors http middleware")
	origins := strings.Split(s.config.AllowedOrigins, ",")
	corsHandler := s.getCorsHandler(origins)
	return corsHandler.CorsMiddleware(next)
}

func useAPIAuthentication(next fasthttp.RequestHandler) fasthttp.RequestHandler {
	token := auth.GetAPIToken()
	if token == "" {
		return next
	}
	log.Info("enabled token authentication on http server")

	return func(ctx *fasthttp.RequestCtx) {
		v := ctx.Request.Header.Peek(auth.APITokenHeader)
		if auth.ExcludedRoute(string(ctx.Request.URI().FullURI())) || string(v) == token {
			ctx.Request.Header.Del(auth.APITokenHeader)
			next(ctx)
		} else {
			ctx.Error("invalid api token", http.StatusUnauthorized)
		}
	}
}

func (s *server) getCorsHandler(allowedOrigins []string) *cors.CorsHandler {
	return cors.NewCorsHandler(cors.Options{
		AllowedOrigins: allowedOrigins,
		Debug:          false,
	})
}

func (s *server) unescapeRequestParametersHandler(next fasthttp.RequestHandler) fasthttp.RequestHandler {
	return func(ctx *fasthttp.RequestCtx) {
		parseError := false
		unescapeRequestParameters := func(parameter []byte, value interface{}) {
			switch value.(type) {
			case string:
				if !parseError {
					parameterValue := fmt.Sprintf("%v", value)
					parameterUnescapedValue, err := url.QueryUnescape(parameterValue)
					if err == nil {
						ctx.SetUserValueBytes(parameter, parameterUnescapedValue)
					} else {
						parseError = true
						errorMessage := fmt.Sprintf("Failed to unescape request parameter %s with value %v. Error: %s", parameter, value, err.Error())
						log.Debug(errorMessage)
						ctx.Error(errorMessage, fasthttp.StatusBadRequest)
					}
				}
			}
		}
		ctx.VisitUserValues(unescapeRequestParameters)

		if !parseError {
			next(ctx)
		}
	}
}

func (s *server) getRouter(endpoints []Endpoint) *routing.Router {
	router := routing.New()
	parameterFinder, _ := regexp.Compile("/{.*}")
	for _, e := range endpoints {
		path := fmt.Sprintf("/%s/%s", e.Version, e.Route)
		for _, m := range e.Methods {
			pathIncludesParameters := parameterFinder.MatchString(path)
			if pathIncludesParameters {
				router.Handle(m, path, s.unescapeRequestParametersHandler(e.Handler))
			} else {
				router.Handle(m, path, e.Handler)
			}
		}
	}
	return router
}
