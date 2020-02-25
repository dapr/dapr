// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	scheme "github.com/dapr/dapr/pkg/client/clientset/versioned"
	"github.com/dapr/dapr/pkg/logger"
	"github.com/gorilla/mux"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const httpPort = 6500

var log = logger.NewLogger("dapr.operator.api")

//Server runs the Dapr API server for components and configurations
type Server interface {
	Run(ctx context.Context)
}

type apiServer struct {
	Client scheme.Interface
}

type Configuration struct {
	Spec ConfigurationSpec `json:"spec,omitempty"`
}

type ConfigurationSpec struct {
	HTTPPipelineSpec PipelineSpec `json:"httpPipeline,omitempty"`
	TracingSpec      TracingSpec  `json:"tracing,omitempty"`
}

type PipelineSpec struct {
	Handlers []HandlerSpec `json:"handlers"`
}

type HandlerSpec struct {
	Name         string       `json:"name"`
	Type         string       `json:"type"`
	SelectorSpec SelectorSpec `json:"selector,omitempty"`
}

type SelectorSpec struct {
	Fields []SelectorField `json:"fields"`
}

type SelectorField struct {
	Field string `json:"field"`
	Value string `json:"value"`
}

type TracingSpec struct {
	Enabled      bool `json:"enabled"`
	ExpandParams bool `json:"expandParams"`
	IncludeBody  bool `json:"includeBody"`
}

// NewAPIServer returns a new API server
func NewAPIServer(client scheme.Interface) Server {
	return &apiServer{
		Client: client,
	}
}

// Run starts a new HTTP control
func (a *apiServer) Run(ctx context.Context) {
	r := mux.NewRouter()
	r.HandleFunc("/components", a.GetComponents).Methods("GET")
	r.HandleFunc("/configurations/{name}", a.GetConfiguration).Methods("GET")
	http.Handle("/", r)

	srv := &http.Server{
		Addr:         fmt.Sprintf(":%d", httpPort),
		Handler:      r,
		WriteTimeout: time.Second * 15,
		ReadTimeout:  time.Second * 15,
		IdleTimeout:  time.Second * 60,
	}

	doneCh := make(chan struct{})

	go func() {
		select {
		case <-ctx.Done():
			log.Info("API server is shutting down")
			shutdownCtx, cancel := context.WithTimeout(
				context.Background(),
				time.Second*5,
			)
			defer cancel()
			srv.Shutdown(shutdownCtx)
		case <-doneCh:
		}
	}()

	err := srv.ListenAndServe()
	if err != nil {
		log.Errorf("API Server error: %s", err)
	}

	close(doneCh)
}

// GetConfiguration returns an Dapr configuration
func (a *apiServer) GetConfiguration(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	name := vars["name"]
	configs, err := a.Client.ConfigurationV1alpha1().Configurations(meta_v1.NamespaceAll).List(meta_v1.ListOptions{})
	if err != nil {
		log.Errorf("Error getting configuration: %s", err)
		RespondWithError(w, 500, fmt.Sprintf("Error getting configurations from kube-client: %s", err))
		return
	}

	for _, c := range configs.Items {
		if c.ObjectMeta.Name == name {
			specData, _ := json.Marshal(c.Spec)
			var spec ConfigurationSpec
			json.Unmarshal(specData, &spec)
			ret := Configuration{
				Spec: spec,
			}
			RespondWithJSON(w, 200, ret)
			return
		}
	}

	RespondWithJSON(w, 200, nil)
}

// GetComponents returns a list of Dapr components
func (a *apiServer) GetComponents(w http.ResponseWriter, r *http.Request) {
	components, err := a.Client.ComponentsV1alpha1().Components(meta_v1.NamespaceAll).List(meta_v1.ListOptions{})
	if err != nil {
		errMsg := fmt.Sprintf("error getting components: %s", err)
		log.Error(errMsg)
		RespondWithError(w, 500, errMsg)
		return
	}

	RespondWithJSON(w, 200, components.Items)
}

// RespondWithError is a helper method for returning an error http message
func RespondWithError(w http.ResponseWriter, code int, message string) {
	RespondWithJSON(w, code, map[string]string{"error": message})
}

// RespondWithJSON is a helper method for returning an HTTP message with a JSON payload
func RespondWithJSON(w http.ResponseWriter, code int, payload interface{}) {
	buffer := &bytes.Buffer{}
	encoder := json.NewEncoder(buffer)
	encoder.SetEscapeHTML(false)
	encoder.Encode(payload)

	bytes := buffer.Bytes()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	w.Write(bytes)
}
