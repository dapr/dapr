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
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	cors "github.com/AdhityaRamadhanus/fasthttpcors"
	routing "github.com/fasthttp/router"
	"github.com/hashicorp/go-multierror"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/pprofhandler"

	"github.com/dapr/dapr/pkg/config"
	corsDapr "github.com/dapr/dapr/pkg/cors"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	diagUtils "github.com/dapr/dapr/pkg/diagnostics/utils"
	httpMiddleware "github.com/dapr/dapr/pkg/middleware/http"
	auth "github.com/dapr/dapr/pkg/runtime/security"
	authConsts "github.com/dapr/dapr/pkg/runtime/security/consts"
	"github.com/dapr/dapr/utils/fasthttpadaptor"
	"github.com/dapr/dapr/utils/nethttpadaptor"
	"github.com/dapr/kit/logger"
)

var (
	log     = logger.NewLogger("dapr.runtime.http")
	infoLog = logger.NewLogger("dapr.runtime.http-info")
)

// Server is an interface for the Dapr HTTP server.
type Server interface {
	io.Closer
	StartNonBlocking() error
}

type server struct {
	config             ServerConfig
	tracingSpec        config.TracingSpec
	metricSpec         config.MetricSpec
	pipeline           httpMiddleware.Pipeline
	api                API
	apiSpec            config.APISpec
	servers            []*fasthttp.Server
	profilingListeners []net.Listener
}

// NewServerOpts are the options for NewServer.
type NewServerOpts struct {
	API         API
	Config      ServerConfig
	TracingSpec config.TracingSpec
	MetricSpec  config.MetricSpec
	Pipeline    httpMiddleware.Pipeline
	APISpec     config.APISpec
}

// NewServer returns a new HTTP server.
func NewServer(opts NewServerOpts) Server {
	infoLog.SetOutputLevel(logger.LogLevel("info"))
	return &server{
		api:         opts.API,
		config:      opts.Config,
		tracingSpec: opts.TracingSpec,
		metricSpec:  opts.MetricSpec,
		pipeline:    opts.Pipeline,
		apiSpec:     opts.APISpec,
	}
}

// StartNonBlocking starts a new server in a goroutine.
func (s *server) StartNonBlocking() error {
	handler := s.useRouter()
	handler = s.useComponents(handler)
	handler = s.useCors(handler)
	handler = useAPIAuthentication(handler)
	handler = s.useMetrics(handler)
	handler = s.useTracing(handler)

	var listeners []net.Listener
	var profilingListeners []net.Listener
	if s.config.UnixDomainSocket != "" {
		socket := fmt.Sprintf("%s/dapr-%s-http.socket", s.config.UnixDomainSocket, s.config.AppID)
		l, err := net.Listen("unix", socket)
		if err != nil {
			return err
		}
		log.Infof("HTTP server listening on UNIX socket: %s", socket)
		listeners = append(listeners, l)
	} else {
		for _, apiListenAddress := range s.config.APIListenAddresses {
			addr := apiListenAddress + ":" + strconv.Itoa(s.config.Port)
			l, err := net.Listen("tcp", addr)
			if err != nil {
				log.Debugf("Failed to listen for HTTP server on TCP address %s with error: %v", addr, err)
			} else {
				log.Infof("HTTP server listening on TCP address: %s", addr)
				listeners = append(listeners, l)
			}
		}
	}
	if len(listeners) == 0 {
		return errors.New("could not listen on any endpoint")
	}

	for _, listener := range listeners {
		// customServer is created in a loop because each instance
		// has a handle on the underlying listener.
		customServer := &fasthttp.Server{
			Handler:               handler,
			MaxRequestBodySize:    s.config.MaxRequestBodySize * 1024 * 1024,
			ReadBufferSize:        s.config.ReadBufferSize * 1024,
			NoDefaultServerHeader: true,
		}
		s.servers = append(s.servers, customServer)

		go func(l net.Listener) {
			if err := customServer.Serve(l); err != nil {
				log.Fatal(err)
			}
		}(listener)
	}

	if s.config.PublicPort != nil {
		publicHandler := s.usePublicRouter()
		publicHandler = s.useMetrics(publicHandler)
		publicHandler = s.useTracing(publicHandler)

		healthServer := &fasthttp.Server{
			Handler:               publicHandler,
			MaxRequestBodySize:    s.config.MaxRequestBodySize * 1024 * 1024,
			NoDefaultServerHeader: true,
		}
		s.servers = append(s.servers, healthServer)

		go func() {
			if err := healthServer.ListenAndServe(fmt.Sprintf(":%d", *s.config.PublicPort)); err != nil {
				log.Fatal(err)
			}
		}()
	}

	if s.config.EnableProfiling {
		for _, apiListenAddress := range s.config.APIListenAddresses {
			addr := apiListenAddress + ":" + strconv.Itoa(s.config.ProfilePort)
			pl, err := net.Listen("tcp", addr)
			if err != nil {
				log.Debugf("Failed to listen for profiling server on TCP address %s with error: %v", addr, err)
			} else {
				log.Infof("HTTP profiling server listening on: %s", addr)
				profilingListeners = append(profilingListeners, pl)
			}
		}

		if len(profilingListeners) == 0 {
			return errors.New("could not listen on any endpoint for profiling API")
		}

		s.profilingListeners = profilingListeners
		for _, listener := range profilingListeners {
			// profServer is created in a loop because each instance
			// has a handle on the underlying listener.
			profServer := &fasthttp.Server{
				Handler:               pprofhandler.PprofHandler,
				MaxRequestBodySize:    s.config.MaxRequestBodySize * 1024 * 1024,
				NoDefaultServerHeader: true,
			}
			s.servers = append(s.servers, profServer)

			go func(l net.Listener) {
				if err := profServer.Serve(l); err != nil {
					log.Fatal(err)
				}
			}(listener)
		}
	}

	return nil
}

func (s *server) Close() error {
	var merr error

	for _, ln := range s.servers {
		// This calls `Close()` on the underlying listener.
		if err := ln.Shutdown(); err != nil {
			merr = multierror.Append(merr, err)
		}
	}

	return merr
}

func (s *server) useTracing(next fasthttp.RequestHandler) fasthttp.RequestHandler {
	if diagUtils.IsTracingEnabled(s.tracingSpec.SamplingRate) {
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

func (s *server) apiLoggingInfo(route string, next fasthttp.RequestHandler) fasthttp.RequestHandler {
	return func(ctx *fasthttp.RequestCtx) {
		fields := make(map[string]any, 2)
		if s.config.APILoggingObfuscateURLs {
			fields["method"] = string(ctx.Method()) + " " + route
		} else {
			fields["method"] = string(ctx.Method()) + " " + string(ctx.Path())
		}
		if userAgent := string(ctx.Request.Header.Peek("User-Agent")); userAgent != "" {
			fields["useragent"] = userAgent
		}

		infoLog.WithFields(fields).Info("HTTP API Called")
		next(ctx)
	}
}

func (s *server) useRouter() fasthttp.RequestHandler {
	endpoints := s.api.APIEndpoints()
	router := s.getRouter(endpoints)

	return router.Handler
}

func (s *server) usePublicRouter() fasthttp.RequestHandler {
	endpoints := s.api.PublicEndpoints()
	router := s.getRouter(endpoints)

	return router.Handler
}

func (s *server) useComponents(next fasthttp.RequestHandler) fasthttp.RequestHandler {
	return fasthttpadaptor.NewFastHTTPHandler(
		s.pipeline.Apply(
			nethttpadaptor.NewNetHTTPHandlerFunc(next),
		),
	)
}

func (s *server) useCors(next fasthttp.RequestHandler) fasthttp.RequestHandler {
	if s.config.AllowedOrigins == corsDapr.DefaultAllowedOrigins {
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
		v := ctx.Request.Header.Peek(authConsts.APITokenHeader)
		if auth.ExcludedRoute(string(ctx.Request.URI().FullURI())) || string(v) == token {
			ctx.Request.Header.Del(authConsts.APITokenHeader)
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
		unescapeRequestParameters := func(parameter []byte, valI interface{}) {
			value, ok := valI.(string)
			if !ok {
				return
			}

			if !parseError {
				parameterUnescapedValue, err := url.QueryUnescape(value)
				if err == nil {
					ctx.SetUserValueBytes(parameter, parameterUnescapedValue)
				} else {
					parseError = true
					errorMessage := fmt.Sprintf("Failed to unescape request parameter %s with value %s. Error: %s", parameter, value, err.Error())
					log.Debug(errorMessage)
					ctx.Error(errorMessage, fasthttp.StatusBadRequest)
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

	// Build the API allowlist and denylist
	allowedAPIs := s.apiSpec.Allowed.ForProtocol("http")
	deniedAPIs := s.apiSpec.Denied.ForProtocol("http")

	for _, e := range endpoints {
		if !e.IsAllowed(allowedAPIs, deniedAPIs) {
			continue
		}

		s.handle(e, parameterFinder, "/"+e.Version+"/"+e.Route, router)

		if e.Alias != "" {
			s.handle(e, parameterFinder, "/"+e.Alias, router)
		}
	}

	return router
}

func (s *server) handle(e Endpoint, parameterFinder *regexp.Regexp, path string, router *routing.Router) {
	pathIncludesParameters := parameterFinder.MatchString(path)

	for _, m := range e.Methods {
		handler := e.Handler

		if pathIncludesParameters && !e.KeepParamUnescape {
			handler = s.unescapeRequestParametersHandler(handler)
		}

		if s.config.EnableAPILogging && (!e.IsHealthCheck || s.config.APILogHealthChecks) {
			handler = s.apiLoggingInfo(path, handler)
		}

		router.Handle(m, path, handler)
	}
}
