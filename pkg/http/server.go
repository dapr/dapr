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
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	// Import pprof that automatically registers itself in the default server mux.
	// Putting "nolint:gosec" here because the linter points out this is automatically exposed on the default server mux, but we only use that in the profiling server.
	//nolint:gosec
	_ "net/http/pprof"

	routing "github.com/fasthttp/router"
	"github.com/go-chi/cors"
	"github.com/valyala/fasthttp"

	"github.com/dapr/dapr/pkg/config"
	corsDapr "github.com/dapr/dapr/pkg/cors"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	diagUtils "github.com/dapr/dapr/pkg/diagnostics/utils"
	httpMiddleware "github.com/dapr/dapr/pkg/middleware/http"
	auth "github.com/dapr/dapr/pkg/runtime/security"
	authConsts "github.com/dapr/dapr/pkg/runtime/security/consts"
	"github.com/dapr/dapr/utils/nethttpadaptor"
	"github.com/dapr/dapr/utils/streams"
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
	servers            []*http.Server
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

	// These middlewares use net/http handlers
	netHTTPHandler := s.useComponents(nethttpadaptor.NewNetHTTPHandlerFunc(handler))
	netHTTPHandler = s.useCors(netHTTPHandler)
	netHTTPHandler = useAPIAuthentication(netHTTPHandler)
	netHTTPHandler = s.useMetrics(netHTTPHandler)
	netHTTPHandler = s.useTracing(netHTTPHandler)
	netHTTPHandler = s.useMaxBodySize(netHTTPHandler)

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
		// srv is created in a loop because each instance
		// has a handle on the underlying listener.
		srv := &http.Server{
			Handler:           netHTTPHandler,
			ReadHeaderTimeout: 10 * time.Second,
			MaxHeaderBytes:    s.config.ReadBufferSizeKB << 10, // To bytes
		}
		s.servers = append(s.servers, srv)

		go func(l net.Listener) {
			if err := srv.Serve(l); err != http.ErrServerClosed {
				log.Fatal(err)
			}
		}(listener)
	}

	if s.config.PublicPort != nil {
		publicHandler := s.usePublicRouter()

		// Convert to net/http
		netHTTPPublicHandler := s.useMetrics(nethttpadaptor.NewNetHTTPHandlerFunc(publicHandler))
		netHTTPPublicHandler = s.useTracing(netHTTPPublicHandler)

		healthServer := &http.Server{
			Addr:              fmt.Sprintf(":%d", *s.config.PublicPort),
			Handler:           netHTTPPublicHandler,
			ReadHeaderTimeout: 10 * time.Second,
			MaxHeaderBytes:    s.config.ReadBufferSizeKB << 10, // To bytes
		}
		s.servers = append(s.servers, healthServer)

		go func() {
			if err := healthServer.ListenAndServe(); err != http.ErrServerClosed {
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
			profServer := &http.Server{
				// pprof is automatically registered in the DefaultServerMux
				Handler:           http.DefaultServeMux,
				ReadHeaderTimeout: 10 * time.Second,
				MaxHeaderBytes:    s.config.ReadBufferSizeKB << 10, // To bytes
			}
			s.servers = append(s.servers, profServer)

			go func(l net.Listener) {
				if err := profServer.Serve(l); err != http.ErrServerClosed {
					log.Fatal(err)
				}
			}(listener)
		}
	}

	return nil
}

func (s *server) Close() error {
	var err error

	for _, ln := range s.servers {
		// This calls `Close()` on the underlying listener.
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		shutdownErr := ln.Shutdown(ctx)
		// Error will be ErrServerClosed if everything went well
		if errors.Is(shutdownErr, http.ErrServerClosed) {
			shutdownErr = nil
		}
		err = errors.Join(err, shutdownErr)
		cancel()
	}

	return err
}

func (s *server) useTracing(next http.Handler) http.Handler {
	if diagUtils.IsTracingEnabled(s.tracingSpec.SamplingRate) {
		log.Infof("enabled tracing http middleware")
		return diag.HTTPTraceMiddleware(next, s.config.AppID, s.tracingSpec)
	}
	return next
}

func (s *server) useMetrics(next http.Handler) http.Handler {
	if s.metricSpec.GetEnabled() {
		log.Infof("enabled metrics http middleware")

		return diag.DefaultHTTPMonitoring.HTTPMiddleware(next.ServeHTTP)
	}

	return next
}

func (s *server) useMaxBodySize(next http.Handler) http.Handler {
	if s.config.MaxRequestBodySizeMB > 0 {
		maxSize := int64(s.config.MaxRequestBodySizeMB) << 20 // To bytes
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = streams.LimitReadCloser(r.Body, maxSize)
			next.ServeHTTP(w, r)
		})
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

func (s *server) useComponents(next http.Handler) http.Handler {
	return s.pipeline.Apply(next)
}

func (s *server) useCors(next http.Handler) http.Handler {
	// TODO: Technically, if "AllowedOrigins" is "*", all origins should be allowed
	// This behavior is not quite correct as in this case we are disallowing all origins
	if s.config.AllowedOrigins == corsDapr.DefaultAllowedOrigins {
		return next
	}

	log.Infof("Enabled cors http middleware")
	return cors.New(cors.Options{
		AllowedOrigins: strings.Split(s.config.AllowedOrigins, ","),
		Debug:          false,
	}).Handler(next)
}

func useAPIAuthentication(next http.Handler) http.Handler {
	token := auth.GetAPIToken()
	if token == "" {
		return next
	}
	log.Info("Enabled token authentication on HTTP server")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		v := r.Header.Get(authConsts.APITokenHeader)
		if auth.ExcludedRoute(r.URL.String()) || v == token {
			r.Header.Del(authConsts.APITokenHeader)
			next.ServeHTTP(w, r)
		} else {
			http.Error(w, "invalid api token", http.StatusUnauthorized)
		}
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
	allowedAPIs := s.apiSpec.Allowed.GetRulesByProtocol(config.APIAccessRuleProtocolHTTP)
	deniedAPIs := s.apiSpec.Denied.GetRulesByProtocol(config.APIAccessRuleProtocolHTTP)

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
