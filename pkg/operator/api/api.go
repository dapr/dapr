package api

import (
    "bytes"
    "context"
    "encoding/json"
    "fmt"
    "net/http"
    "time"

    log "github.com/Sirupsen/logrus"
    components_v1alpha1 "github.com/actionscore/actions/pkg/apis/components/v1alpha1"
    scheme "github.com/actionscore/actions/pkg/client/clientset/versioned"
    "github.com/gorilla/mux"
    meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var httpPort = 6500

type APIServer interface {
    Run(ctx context.Context)
}

type apiServer struct {
    Client scheme.Interface
}

type Component struct {
    Metadata ComponentMetadata                 `json:"metadata"`
    Spec     components_v1alpha1.ComponentSpec `json:"spec"`
}

type ComponentMetadata struct {
    Name string `json:"name"`
}

type Configuration struct {
    Spec ConfigurationSpec `json:"spec,omitempty"`
}

type ConfigurationSpec struct {
    TracingSpec TracingSpec `json:"tracing,omitempty"`
}

type TracingSpec struct {
    Enabled          bool   `json:"enabled"`
    ExporterType     string `json:"exporterType"`
    ExporterAddress  string `json:"exporterAddress"`
    IncludeEvent     bool   `json:"includeEvent"`
    IncludeEventBody bool   `json:"includeEventBody"`
}

// NewAPIServer returns a new API server
func NewAPIServer(client scheme.Interface) APIServer {
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

// GetConfiguration returns an Actions configuration
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
            ret := Configuration{
                Spec: ConfigurationSpec{
                    TracingSpec: TracingSpec{
                        Enabled:          c.Spec.TracingSpec.Enabled,
                        ExporterType:     c.Spec.TracingSpec.ExporterType,
                        ExporterAddress:  c.Spec.TracingSpec.ExporterAddress,
                        IncludeEvent:     c.Spec.TracingSpec.IncludeEvent,
                        IncludeEventBody: c.Spec.TracingSpec.IncludeEventBody,
                    },
                },
            }
            RespondWithJSON(w, 200, ret)
            return
        }
    }

    RespondWithJSON(w, 200, nil)
}

// GetComponents returns a list of Actions components
func (a *apiServer) GetComponents(w http.ResponseWriter, r *http.Request) {
    list := []Component{}

    components, err := a.Client.ComponentsV1alpha1().Components(meta_v1.NamespaceAll).List(meta_v1.ListOptions{})
    if err != nil {
        log.Errorf("Error getting components: %s", err)
        RespondWithError(w, 500, "Error occured")
        return
    }

    for _, c := range components.Items {
        list = append(list, Component{
            Metadata: ComponentMetadata{
                Name: c.ObjectMeta.Name,
            },
            Spec: c.Spec,
        })
    }

    RespondWithJSON(w, 200, list)
}

// RespondWithError is a helper method for returning an error http message
func RespondWithError(w http.ResponseWriter, code int, message string) {
    RespondWithJSON(w, code, map[string]string{"error": message})
}

// RespondWithJSON is a helper method for returning an HTTP message with a JSON paylad
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


