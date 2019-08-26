package http

import (
	"fmt"
	"strings"

	cors "github.com/AdhityaRamadhanus/fasthttpcors"
	log "github.com/Sirupsen/logrus"
	"github.com/actionscore/actions/pkg/config"
	diag "github.com/actionscore/actions/pkg/diagnostics"
	routing "github.com/qiangxue/fasthttp-routing"
	"github.com/valyala/fasthttp"
)

// Server is an interface for the Actions HTTP server
type Server interface {
	StartNonBlocking()
}

type server struct {
	config      ServerConfig
	tracingSpec config.TracingSpec
	api         API
}

// NewServer returns a new HTTP server
func NewServer(api API, config ServerConfig, tracingSpec config.TracingSpec) Server {
	return &server{
		api:         api,
		config:      config,
		tracingSpec: tracingSpec,
	}
}

// StartNonBlocking starts a new server in a goroutine
func (s *server) StartNonBlocking() {
	endpoints := s.api.APIEndpoints()
	router := s.getRouter(endpoints)
	origins := strings.Split(s.config.AllowedOrigins, ",")
	corsHandler := s.getCorsHandler(origins)

	go func() {
		if s.tracingSpec.Enabled {
			diag.CreateExporter(s.config.ActionID, s.config.HostAddress, s.tracingSpec, nil)
			log.Fatal(fasthttp.ListenAndServe(fmt.Sprintf(":%v", s.config.Port),
				diag.TracingHTTPMiddleware(s.tracingSpec, corsHandler.CorsMiddleware(router.HandleRequest))))
		} else {
			log.Fatal(fasthttp.ListenAndServe(fmt.Sprintf(":%v", s.config.Port), corsHandler.CorsMiddleware(router.HandleRequest)))
		}
	}()
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
