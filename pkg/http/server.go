// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package http

import (
	"fmt"
	"strings"

	"github.com/valyala/fasthttp/pprofhandler"

	cors "github.com/AdhityaRamadhanus/fasthttpcors"
	log "github.com/Sirupsen/logrus"
	"github.com/dapr/dapr/pkg/config"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	http_middleware "github.com/dapr/dapr/pkg/middleware/http"
	routing "github.com/qiangxue/fasthttp-routing"
	"github.com/valyala/fasthttp"
)

// Server is an interface for the Dapr HTTP server
type Server interface {
	StartNonBlocking()
}

type server struct {
	config      ServerConfig
	tracingSpec config.TracingSpec
	pipeline    http_middleware.HTTPPipeline
	api         API
}

// NewServer returns a new HTTP server
func NewServer(api API, config ServerConfig, tracingSpec config.TracingSpec, pipeline http_middleware.HTTPPipeline) Server {
	return &server{
		api:         api,
		config:      config,
		tracingSpec: tracingSpec,
		pipeline:    pipeline,
	}
}

// StartNonBlocking starts a new server in a goroutine
func (s *server) StartNonBlocking() {
	endpoints := s.api.APIEndpoints()
	router := s.getRouter(endpoints)
	origins := strings.Split(s.config.AllowedOrigins, ",")
	corsHandler := s.getCorsHandler(origins)
	handler := s.pipeline.Apply(router.HandleRequest)
	go func() {
		if s.tracingSpec.Enabled {
			log.Fatal(fasthttp.ListenAndServe(fmt.Sprintf(":%v", s.config.Port),
				s.pipeline.Apply(diag.TracingHTTPMiddleware(s.tracingSpec, corsHandler.CorsMiddleware(handler)))))
		} else {
			log.Fatal(fasthttp.ListenAndServe(fmt.Sprintf(":%v", s.config.Port),
				s.pipeline.Apply(corsHandler.CorsMiddleware(handler))))
		}
	}()

	if s.config.EnableProfiling {
		go func() {
			log.Infof("starting profiling server on port %v", s.config.ProfilePort)
			log.Fatal(fasthttp.ListenAndServe(fmt.Sprintf(":%v", s.config.ProfilePort), pprofhandler.PprofHandler))
		}()
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
