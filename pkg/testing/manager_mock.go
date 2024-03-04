package testing

import (
	"context"
	"net/http"
	"reflect"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/record"

	"github.com/dapr/dapr/pkg/testing/logging"

	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
)

func NewMockManager() *MockManager {
	mgr := &MockManager{
		sc:        runtime.NewScheme(),
		log:       logr.New(logging.NullLogSink{}),
		runnables: []manager.Runnable{},
		indexer: &MockFieldIndexer{
			typeMap: map[reflect.Type]client.IndexerFunc{},
		},
	}

	return mgr
}

type MockManager struct {
	sc        *runtime.Scheme
	log       logr.Logger
	runnables []manager.Runnable
	indexer   *MockFieldIndexer
}

type MockFieldIndexer struct {
	typeMap map[reflect.Type]client.IndexerFunc
}

func (m *MockManager) GetRunnables() []manager.Runnable {
	return m.runnables
}

func (m *MockManager) Add(runnable manager.Runnable) error {
	m.runnables = append(m.runnables, runnable)
	return nil
}

func (m *MockManager) Elected() <-chan struct{} {
	return nil
}

func (m *MockManager) SetFields(interface{}) error {
	return nil
}

func (m *MockManager) AddMetricsExtraHandler(path string, handler http.Handler) error {
	return nil
}

func (m *MockManager) AddHealthzCheck(name string, check healthz.Checker) error {
	return nil
}

func (m *MockManager) AddReadyzCheck(name string, check healthz.Checker) error {
	return nil
}

func (m *MockManager) Start(ctx context.Context) error {
	return nil
}

func (m *MockManager) GetConfig() *rest.Config {
	return nil
}

func (m *MockManager) GetControllerOptions() config.Controller {
	return config.Controller{}
}

func (m *MockManager) GetScheme() *runtime.Scheme {
	return m.sc
}

func (m *MockManager) GetClient() client.Client {
	return nil
}

func (m *MockManager) GetFieldIndexer() client.FieldIndexer {
	return m.indexer
}

func (m *MockManager) GetCache() cache.Cache {
	return nil
}

func (m *MockManager) GetEventRecorderFor(name string) record.EventRecorder {
	return nil
}

func (m *MockManager) GetRESTMapper() meta.RESTMapper {
	return nil
}

func (m *MockManager) GetAPIReader() client.Reader {
	return nil
}

func (m *MockManager) GetWebhookServer() webhook.Server {
	return nil
}

func (m *MockManager) GetLogger() logr.Logger {
	return m.log
}

func (m *MockManager) GetHTTPClient() *http.Client {
	return http.DefaultClient
}

func (t *MockFieldIndexer) IndexField(ctx context.Context, obj client.Object, field string, extractValue client.IndexerFunc) error {
	t.typeMap[reflect.TypeOf(obj)] = extractValue
	return nil
}

func (m *MockManager) GetIndexerFunc(obj client.Object) client.IndexerFunc {
	return m.indexer.typeMap[reflect.TypeOf(obj)]
}
