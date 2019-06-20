package action

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/golang/protobuf/ptypes/empty"
	jsoniter "github.com/json-iterator/go"
	"github.com/valyala/fasthttp"

	"github.com/golang/protobuf/ptypes/any"

	cors "github.com/AdhityaRamadhanus/fasthttpcors"
	log "github.com/Sirupsen/logrus"
	configuration_v1alpha1 "github.com/actionscore/actions/pkg/apis/configuration/v1alpha1"
	eventing_v1alpha1 "github.com/actionscore/actions/pkg/apis/eventing/v1alpha1"
	"github.com/actionscore/actions/pkg/consistenthash"
	diag "github.com/actionscore/actions/pkg/diagnostics"
	exporters "github.com/actionscore/actions/pkg/exporters"
	pb "github.com/actionscore/actions/pkg/proto"
	"github.com/ghodss/yaml"
	"github.com/gorilla/mux"
	routing "github.com/qiangxue/fasthttp-routing"
	"go.opencensus.io/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

type actionMode string
type concurrencyMode string
type interactionMode string
type invokedHTTPMethod string
type Protocol string

const (
	KubernetesMode        actionMode      = "kubernetes"
	StandaloneMode        actionMode      = "standalone"
	FireForgetCall        interactionMode = "fireforget"
	WaitCall              interactionMode = "wait"
	parallelMode          concurrencyMode = "parallel"
	HTTPProtocol          Protocol        = "http"
	GRPCProtocol          Protocol        = "grpc"
	stateStoreType                        = "statestore"
	sender                                = "sender"
	contextIDHeader                       = "actions.context-id"
	contextIDPath                         = "contextid"
	interactionModeHeader                 = "actions.interaction"
	fromHeader                            = "actions.from"
	actionIDHeader                        = "id"
	invokedMethodHeader                   = "method"
	invokedHTTPVerbHeader                 = "http.method"
	actionTargetIDHeader                  = "actions.target-id"
	addressHeader                         = "actions.action-address"
	zipkinExporter                        = "zipkin"
)

var (
	gRPCPort int
)

type Action struct {
	Router              *mux.Router
	ActionID            string
	ApplicationPort     string
	Mode                string
	Protocol            Protocol
	EventSources        []EventSource
	ActionSources       map[string]ActionSource
	StateStore          ActionSource
	EventSourcesPath    string
	ConfigurationName   string
	APIAddress          string
	AssignerAddress     string
	AppConfig           ApplicationConfig
	Sender              Sender
	StateWriteLock      *sync.Mutex
	HTTPClient          *fasthttp.Client
	GRPCClient          *grpc.ClientConn
	GRPCLock            *sync.Mutex
	GRPCConnectionPool  map[string]*grpc.ClientConn
	IPAddress           string
	AssignmentTableLock *sync.RWMutex
	AssignmentTables    *consistenthash.AssignmentTables
	AssignmentSignal    chan struct{}
	AssignmentBlock     bool
	ActiveContextsLock  *sync.RWMutex
	ActiveContexts      map[string]string
	json                jsoniter.API
	Configuration       Configuration
	AllowedOrigins      []string
}

func NewAction(actionID string, applicationPort string, mode string, protocol string, eventSourcesPath string, configurationName string, apiAddress string, assignerAddress string, allowedOrigins string) *Action {
	http.DefaultTransport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true}

	return &Action{
		ActionID:            actionID,
		ApplicationPort:     applicationPort,
		Protocol:            Protocol(protocol),
		Router:              mux.NewRouter(),
		Mode:                mode,
		EventSourcesPath:    eventSourcesPath,
		ConfigurationName:   configurationName,
		APIAddress:          apiAddress,
		AssignerAddress:     assignerAddress,
		StateWriteLock:      &sync.Mutex{},
		HTTPClient:          &fasthttp.Client{MaxConnsPerHost: 1000000, TLSConfig: &tls.Config{InsecureSkipVerify: true}},
		GRPCLock:            &sync.Mutex{},
		GRPCConnectionPool:  map[string]*grpc.ClientConn{},
		AssignmentTableLock: &sync.RWMutex{},
		AssignmentTables:    &consistenthash.AssignmentTables{Entries: make(map[string]*consistenthash.Consistent)},
		ActiveContextsLock:  &sync.RWMutex{},
		ActiveContexts:      map[string]string{},
		json:                jsoniter.ConfigFastest,
		AllowedOrigins:      strings.Split(allowedOrigins, ","),
	}
}

func (i *Action) GetConfiguration(name string) (Configuration, error) {
	ret := Configuration{
		Spec: ConfigurationSpec{
			TracingSpec: TracingSpec{
				Enabled: false,
			},
		},
	}
	if name == "" {
		return ret, nil
	}
	if i.Mode == string(KubernetesMode) {
		url := fmt.Sprintf("%s/configurations/%s", i.APIAddress, name)
		res, err := http.DefaultClient.Get(url)
		if err != nil {
			log.Errorf("Error getting configuration: %s", err)
			return ret, err
		}

		err = i.json.NewDecoder(res.Body).Decode(&ret)
		if err != nil {
			log.Errorf("Error decoding configuration: %s", err)
			return ret, err
		}
		return ret, nil
	} else {
		b, err := ioutil.ReadFile(i.ConfigurationName)
		if err != nil {
			return ret, nil
		}
		var cf configuration_v1alpha1.Configuration
		err = yaml.Unmarshal(b, &cf)
		if err != nil {
			return ret, nil
		} else {
			return Configuration{
				Spec: ConfigurationSpec{
					TracingSpec: TracingSpec{
						Enabled:          cf.Spec.TracingSpec.Enabled,
						ExporterType:     cf.Spec.TracingSpec.ExporterType,
						ExporterAddress:  cf.Spec.TracingSpec.ExporterAddress,
						IncludeEvent:     cf.Spec.TracingSpec.IncludeEvent,
						IncludeEventBody: cf.Spec.TracingSpec.IncludeEventBody,
					},
				},
			}, nil
		}
	}
}
func (i *Action) GetEventSources() []EventSource {
	var eventSources []EventSource

	if i.Mode == string(KubernetesMode) {
		url := fmt.Sprintf("%s/eventsources", i.APIAddress)
		res, err := http.DefaultClient.Get(url)
		if err != nil {
			log.Errorf("Error getting event sources: %s", err)
			return eventSources
		}

		err = i.json.NewDecoder(res.Body).Decode(&eventSources)
		if err != nil {
			log.Errorf("Error decoding event sources: %s", err)
			return eventSources
		}
	} else {
		dir := i.EventSourcesPath
		files, err := ioutil.ReadDir(dir)
		if err != nil {
			log.Error(err)
		} else {
			for _, f := range files {
				b, err := ioutil.ReadFile(fmt.Sprintf("%s/%s", dir, f.Name()))
				if err != nil {
					log.Error(err)
				} else {
					var es eventing_v1alpha1.EventSource
					err := yaml.Unmarshal(b, &es)
					if err != nil {
						log.Error(err)
					} else {
						eventSources = append(eventSources, EventSource{
							Name: es.ObjectMeta.Name,
							Spec: EventSourceSpec{
								Type:           es.Spec.Type,
								ConnectionInfo: es.Spec.ConnectionInfo,
								SenderOptions:  es.Spec.SenderOptions,
							},
						})
					}
				}
			}
		}
	}

	return eventSources
}
func (i *Action) ConfigureTracing() {
	cfg, err := i.GetConfiguration(i.ConfigurationName)
	if err != nil {
		log.Errorf("Error configuring Actions: %s", err)
		return
	}
	i.Configuration = cfg
	switch cfg.Spec.TracingSpec.ExporterType {
	case zipkinExporter:
		ex := exporters.ZipkinExporter{}
		ex.Init(i.ActionID, fmt.Sprintf("%s:%d", i.IPAddress, gRPCPort), cfg.Spec.TracingSpec.ExporterAddress)
	default:
		return
	}
}
func (i *Action) SetupEventSources() {
	i.EventSources = i.GetEventSources()

	for _, es := range i.EventSources {
		log.Infof("Discovered EventSource %s (%s)", es.Name, es.Spec.Type)

		if es.Name == stateStoreType {
			continue
		} else if i.Protocol == GRPCProtocol {
			continue
		}

		endpoint := i.GetLocalAppEndpoint(es.Name)
		req, _ := http.NewRequest(http.MethodOptions, endpoint, nil)
		res, err := http.DefaultClient.Do(req)
		if err == nil && res.StatusCode != 404 {
			reader := i.GetActionSource(es.Spec.Type)
			err := reader.Init(es.Spec)
			if err != nil {
				log.Errorf("Error from reader Init: %s", err)
				return
			}
			if es.Spec.SenderOptions.QueueName != "" {
				//TODO: allow different sender types
				es.Sender = NewRedisSender()

				if err = es.Sender.Init(es.Spec); err != nil {
					log.Errorf("Error from reader Init - %s", err)
					return
				}
				es.Sender.StartLoop(i.PostEventToAppWithRetries, context.Background())
			}
			if reader != nil {
				i.ReadEvents(es.Name, reader, es.Sender)
			}
		}
	}
}

func (i *Action) StartGRPCServer(grpcPort int) {
	gRPCPort = grpcPort

	log.Info(fmt.Sprintf("Starting gRPC server on port %v", grpcPort))
	go func() {
		lis, err := net.Listen("tcp", fmt.Sprintf(":%v", grpcPort))
		if err != nil {
			log.Fatalf("gRPC error: %s", err)
		}
		s := grpc.NewServer()
		pb.RegisterActionServer(s, i)
		if err := s.Serve(lis); err != nil {
			log.Fatalf("gRPC error: %v", err)
		}
	}()
}

func (i *Action) ReadEvents(eventName string, reader ActionSource, sender Sender) {
	go func() {
		ctx, span := trace.StartSpan(context.Background(), "read-event")
		if span != nil {
			defer span.End()
		}
		//TBD: carry over context from message

		err := reader.ReadAsync(nil, func(data []byte) error {
			var body interface{}
			err := i.json.Unmarshal(data, &body)
			if err != nil {
				log.Errorf("Error Parsing event: %s", err)
				return err
			}

			event := Event{
				Data:      body,
				EventName: eventName,
				CreatedAt: time.Now(),
			}
			if sender != nil {
				return sender.Enqueue(event)
			}

			return i.PostEventToAppWithRetries(&[]Event{event}, ctx)
		})
		if err != nil {
			log.Errorf("Error from event reader. event name: %s: %s", eventName, err)
		}
	}()
}

func (i *Action) PostEventToAppWithRetries(events *[]Event, c context.Context) error {

	_, span, spanCtx := i.TraceSpanFromContext(c, *events, "PostEventToAppWithRetires")
	if span != nil {
		defer span.End()
	}

	sent := false
	timeStarted := time.Now()
	timeoutSeconds := time.Second * 30

	for !sent {
		if time.Since(timeStarted).Seconds() >= timeoutSeconds.Seconds() {

			diag.SetSpanStatus(span, trace.StatusCodeDeadlineExceeded, fmt.Sprintf("failed to dispath message to application after %d seconds", 30))

			return fmt.Errorf("Timed out while sending message to app")
		}
		hasError := false
		for _, event := range *events {
			_, err := i.PostEventToApp(Event{
				Data:      event.Data,
				CreatedAt: time.Now(),
				EventName: event.EventName,
			}, spanCtx)
			if err != nil {
				hasError = true

				diag.SetSpanStatus(span, trace.StatusCodeDataLoss, err.Error())

				break
			}
		}
		if !hasError {
			sent = true

			diag.SetSpanStatus(span, trace.StatusCodeOK, "message dispatched to application")

			break
		}
		time.Sleep(time.Duration(500) * time.Millisecond)
	}
	return nil
}

func (i *Action) GetEventSourceSpec(name string) (*EventSourceSpec, error) {
	for _, es := range i.EventSources {
		if es.Name == name {
			return &es.Spec, nil
		}
	}

	return nil, nil
}

func (i *Action) GetLocalAppEndpoint(path string) string {
	base := "127.0.0.1"
	if i.Protocol == GRPCProtocol {
		return fmt.Sprintf("%s:%s", base, i.ApplicationPort)
	}

	return fmt.Sprintf("http://%s:%s/%s", base, i.ApplicationPort, path)
}

func (i *Action) PostEventToApp(event Event, ctx *trace.SpanContext) (*Event, error) {
	url := i.GetLocalAppEndpoint(event.EventName)
	res, err := i.SendEventViaHTTP(url, event, ctx)
	if err != nil {
		log.Errorf("Error sending data to app: %s", err)
		return nil, err
	} else if res != nil {
		i.ActOnEventFromApp(*res, ctx)
		return res, nil
	}
	return res, nil
}

func (i *Action) SendStateViaHTTP(url string, state []KeyValState, ctx *trace.SpanContext) error {
	b := new(bytes.Buffer)
	i.json.NewEncoder(b).Encode(state)

	_, err := i.ExecuteHTTPCall(url, http.MethodPost, nil, b.Bytes(), ctx)
	if err != nil {
		return err
	}

	return nil
}

func (i *Action) ExecuteHTTPCall(url, httpMethod string, headers map[string]string, payload []byte, ctx *trace.SpanContext) ([]byte, error) {
	req := fasthttp.AcquireRequest()
	req.SetRequestURI(url)
	req.SetBody(payload)
	req.Header.SetContentType("application/json")
	req.Header.SetMethod(strings.ToUpper(httpMethod))

	if ctx != nil {
		req.Header.Add(diag.CorrelationID, diag.SerializeSpanContext(*ctx))
	}
	if headers != nil {
		for k, v := range headers {
			req.Header.Set(k, v)
		}
	}

	resp := fasthttp.AcquireResponse()
	err := i.HTTPClient.Do(req, resp)
	if err != nil {
		return nil, err
	}

	body := resp.Body()
	arr := make([]byte, len(body))
	copy(arr, body)
	fasthttp.ReleaseRequest(req)
	fasthttp.ReleaseResponse(resp)
	return arr, nil
}

func (i *Action) SendEventViaHTTP(url string, event Event, ctx *trace.SpanContext) (*Event, error) {
	b := new(bytes.Buffer)
	i.json.NewEncoder(b).Encode(event)

	res, err := i.ExecuteHTTPCall(url, "POST", nil, b.Bytes(), ctx)
	if err != nil {
		return nil, err
	}
	if res == nil || len(res) == 0 {
		return nil, nil
	}

	var response Event
	i.json.Unmarshal(res, &response)
	if err != nil {
		return nil, err
	}

	return &response, nil
}

func (i *Action) GetActionSource(sourceType string) ActionSource {
	if val, ok := i.ActionSources[sourceType]; ok {
		return val
	}

	return nil
}

func (i *Action) SaveActorStateToStore(state KeyValState) error {
	if i.StateStore == nil {
		return nil
	}

	err := i.StateStore.Write(state)
	if err != nil {
		return err
	}

	return nil
}

func (i *Action) SaveStateToStore(state []KeyValState) error {
	if i.StateStore != nil && len(state) > 0 {
		for _, s := range state {
			actionKey := fmt.Sprintf("%s-%s", i.ActionID, s.Key)
			s.Key = actionKey
			err := i.StateStore.Write(s)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func (i *Action) GetStateFromStore(key string) (interface{}, error) {
	if i.StateStore != nil && key != "" {
		data, err := i.StateStore.Read(key)
		if err != nil {
			return nil, err
		}

		return data, nil
	}

	return nil, nil
}

func (i *Action) SendEventToTarget(target string, event Event, ctx *trace.SpanContext) error {
	eventSourceSpec, err := i.GetEventSourceSpec(target)
	if err == nil && eventSourceSpec != nil && eventSourceSpec.Type != "" {
		writer := i.GetActionSource(eventSourceSpec.Type)
		err := writer.Init(*eventSourceSpec)
		if err != nil {
			return fmt.Errorf("Error from writer Init: %s", err)
		}

		if writer != nil {
			err := writer.Write(event.Data)
			if err != nil {
				return fmt.Errorf("Error from event writer %s: %s", eventSourceSpec.Type, err)
			}
		} else {
			log.Infof("Couldn't find event writer of type %s", target)
		}
	} else {
		url := ""
		if i.Mode == string(KubernetesMode) {
			url = fmt.Sprintf("http://%s-action.default.svc.cluster.local/publish", target)
		} else if i.Mode == string(StandaloneMode) {
			url = fmt.Sprintf("http://%s/publish", target)
		}

		event := Event{
			EventName: event.EventName,
			Data:      event.Data,
			CreatedAt: time.Now(),
		}

		_, err := i.SendEventViaHTTP(url, event, ctx)
		if err != nil {
			return fmt.Errorf("Error sending event %s to %s: %s", event.EventName, target, err)
		}
	}

	return nil
}

func (i *Action) ActOnEventFromApp(response Event, ctx *trace.SpanContext) {
	if len(response.State) > 0 {
		go func() {
			err := i.SaveStateToStore(response.State)
			if err != nil {
				log.Errorf("Error saving state: %s", err)
			}
		}()
	}

	if response.Concurrency == string(parallelMode) {
		for _, target := range response.To {
			i.SendEventToTargetAsync(target, response, ctx)
		}
	} else {
		for _, target := range response.To {
			err := i.SendEventToTarget(target, response, ctx)
			if err != nil {
				log.Error(err)
			}
		}
	}
}

func (i *Action) SendEventToTargetAsync(target string, event Event, ctx *trace.SpanContext) {
	go func() {
		err := i.SendEventToTarget(target, event, ctx)
		if err != nil {
			log.Error(err)
		}
	}()
}

// OnEventPublished is called when the user code publishes a message through Actions /publish route
func (i *Action) OnEventPublished(c *routing.Context) {

	ctx, span, spanCtx := i.TraceSpanFromRoutingContext(c, nil, "OnEventPublished")
	if span != nil {
		defer span.End()
	}

	body := c.RequestCtx.PostBody()
	if body != nil {
		var event Event
		err := i.json.Unmarshal(body, &event)
		if err != nil {
			log.Errorf("Error decoding body on event: %s", err)

			diag.SetSpanStatus(span, trace.StatusCodeDataLoss, err.Error())

			c.RequestCtx.SetStatusCode(500)
			c.RequestCtx.Write(nil)
			return
		}

		if len(event.To) > 0 {
			i.ActOnEventFromApp(event, spanCtx)
		} else {
			if i.Sender != nil {
				err = i.Sender.Enqueue(event)
			} else {
				err = i.PostEventToAppWithRetries(&[]Event{event}, ctx)
			}
			if err != nil {

				diag.SetSpanStatus(span, trace.StatusCodeDataLoss, err.Error())

				c.RequestCtx.SetStatusCode(500)
				c.RequestCtx.Write(nil)
				return
			}
		}
	}

	diag.SetSpanStatus(span, trace.StatusCodeOK, "message is dispatched to application")

	c.RequestCtx.SetStatusCode(200)
	c.RequestCtx.Write(nil)
}

func (i *Action) GetGRPCConnection(address string) (*grpc.ClientConn, error) {
	if val, ok := i.GRPCConnectionPool[address]; ok {
		return val, nil
	}

	i.GRPCLock.Lock()
	if val, ok := i.GRPCConnectionPool[address]; ok {
		i.GRPCLock.Unlock()
		return val, nil
	}

	conn, err := grpc.Dial(address, grpc.WithInsecure())
	if err != nil {
		i.GRPCLock.Unlock()
		return nil, err
	}

	i.GRPCConnectionPool[address] = conn
	i.GRPCLock.Unlock()

	return conn, nil
}

func (i *Action) OnLocalBatchActivation(c *routing.Context) {
	actionID := c.Param(actionIDHeader)
	payload := c.RequestCtx.PostBody()
	var contextIDs []string
	err := json.Unmarshal(payload, &contextIDs)
	if err != nil {
		RespondWithError(c.RequestCtx, 500, "Error deserializing context IDs")
		return
	}

	needActivation := []string{}
	i.ActiveContextsLock.RLock()
	for _, c := range contextIDs {
		address := i.LookupContextAddress(actionID, c)
		if address == fmt.Sprintf("%s:%v", i.IPAddress, gRPCPort) {
			if _, ok := i.ActiveContexts[c]; !ok {
				needActivation = append(needActivation, c)
			} else {
				//TODO: remote activation
			}
		}
	}
	i.ActiveContextsLock.RUnlock()

	if len(needActivation) == 0 {
		RespondEmpty(c.RequestCtx, 200)
		return
	}

	contexts := []ContextActivation{}
	for _, c := range needActivation {
		contextKey := fmt.Sprintf("%s-%s", actionID, c)
		state, _ := i.GetStateFromStore(contextKey)
		payload := "{}"
		if state != nil && state.(string) != "" {
			payload = state.(string)
		}

		contexts = append(contexts, ContextActivation{
			ID:    c,
			State: []byte(payload),
		})
	}

	b, _ := i.json.Marshal(&contexts)
	url := i.GetLocalAppEndpoint(fmt.Sprintf("actors/%s/batch", actionID))
	r, err := i.ExecuteHTTPCall(url, http.MethodPost, nil, b, nil)
	if err != nil {
		RespondWithError(c.RequestCtx, 500, "error activating batched contexts")
		return
	}

	activated := []string{}
	err = json.Unmarshal(r, &activated)
	if err != nil {
		RespondWithError(c.RequestCtx, 500, "error activating batched contexts")
		return
	}

	i.ActiveContextsLock.Lock()
	defer i.ActiveContextsLock.Unlock()
	for _, a := range activated {
		i.ActiveContexts[a] = actionID
	}

	RespondEmpty(c.RequestCtx, 200)
}

func (i *Action) OnInvokeAction(c *routing.Context) {

	_, span, spanCtx := i.TraceSpanFromRoutingContext(c, nil, "OnInvokeAction")
	if span != nil {
		defer span.End()
	}

	actionID := c.Param(actionIDHeader)
	actionMethod := c.Param(invokedMethodHeader)
	interaction := string(c.Request.Header.Peek(interactionModeHeader))
	contextID := c.Param(contextIDPath)
	targetAddress := string(c.Request.Header.Peek(addressHeader))
	httpMethod := string(c.Method())

	corId := ""
	if span != nil {
		corId = diag.SerializeSpanContext(*spanCtx)
	}

	headers := map[string]string{
		fromHeader:            i.ActionID,
		invokedHTTPVerbHeader: httpMethod,
		invokedMethodHeader:   actionMethod,
		actionTargetIDHeader:  actionID,
		diag.CorrelationID:    corId,
	}

	if contextID != "" {
		headers[contextIDHeader] = contextID
		// Only block if request is targeting a context and an assignment table update is ongoing
		if i.AssignmentBlock {
			<-i.AssignmentSignal
		}
	}

	responseDelivered := false
	if interaction == string(FireForgetCall) {
		headers[interactionModeHeader] = interaction

		diag.SetSpanStatus(span, trace.StatusCodeOK, "fire and forget call dispatched")

		RespondEmpty(c.RequestCtx, 200)
		responseDelivered = true
	}

	payload := c.RequestCtx.PostBody()
	address := ""
	if targetAddress != "" {
		address = targetAddress
	} else {
		if contextID != "" {
			address = i.LookupContextAddress(actionID, contextID)

			if address == "" {
				if !responseDelivered {

					diag.SetSpanStatus(span, trace.StatusCodeUnavailable, fmt.Sprintf("could not locate host for: %s/%s", actionID, contextID))

					RespondWithError(c.RequestCtx, 500, fmt.Sprintf("could not locate host for: %s/%s", actionID, contextID))
				}

				return
			}
		} else {
			address = fmt.Sprintf("%s-action.default.svc.cluster.local:%v", actionID, gRPCPort)
		}
	}

	var resp []byte
	var err error

	if address == fmt.Sprintf("%s:%v", i.IPAddress, gRPCPort) {
		resp, err = i.invoke(actionMethod, actionID, contextID, "", httpMethod, string(c.URI().QueryString()), payload, spanCtx)
	} else {
		conn, err := i.GetGRPCConnection(address)
		if err != nil {
			log.Fatalf("gRPC connection failure: %s", err)
			if !responseDelivered {

				diag.SetSpanStatus(span, trace.StatusCodeInternal, fmt.Sprintf("Delivery to action id %s failed - %s", actionID, err.Error()))

				RespondWithError(c.RequestCtx, 500, fmt.Sprintf("delivery to action id %s failed", actionID))
			}
			return
		}

		md := metadata.New(headers)
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute*1)
		defer cancel()

		ctxMetadata := metadata.NewOutgoingContext(ctx, md)
		client := pb.NewActionClient(conn)
		r, err := client.Invoke(ctxMetadata, &pb.InvokeEnvelope{Data: &any.Any{Value: payload}})
		if r != nil && r.Data != nil {
			resp = r.GetData().GetValue()
			responseDelivered = true
		}
	}

	if err != nil {
		log.Error(err)

		diag.SetSpanStatus(span, trace.StatusCodeInternal, fmt.Sprintf("Delivery to action id %s failed - %s", actionID, err.Error()))

		RespondWithError(c.RequestCtx, 500, fmt.Sprintf("delivery to action id %s failed", actionID))
	} else {
		if resp != nil && len(resp) > 0 {

			diag.SetSpanStatus(span, trace.StatusCodeOK, fmt.Sprintf("action was called and returned %s", string(resp)))

			RespondWithJSON(c.RequestCtx, 200, resp)
		} else {
			diag.SetSpanStatus(span, trace.StatusCodeOK, fmt.Sprintf("action was called but returned no value"))

			RespondEmpty(c.RequestCtx, 200)
		}
	}
}

func (i *Action) SaveState(ctx context.Context, in *pb.SaveStateEnvelope) (*empty.Empty, error) {
	states := []KeyValState{}
	for _, s := range in.State {
		var v interface{}
		err := i.json.Unmarshal(s.Value.Value, &v)
		if err != nil {
			return nil, err
		}

		k := KeyValState{
			Key:   s.Key,
			Value: v,
		}

		states = append(states, k)
	}

	err := i.SaveStateToStore(states)
	if err != nil {
		return nil, err
	}

	return &empty.Empty{}, nil
}

func (i *Action) OnStatePosted(c *routing.Context) {
	var state []KeyValState
	err := i.json.Unmarshal(c.RequestCtx.PostBody(), &state)
	if err != nil {
		log.Errorf("Error deserializing data from OnStatePosted: %s", err)
		RespondWithError(c.RequestCtx, 500, "Error saving state")
		return
	}

	err = i.SaveStateToStore(state)
	if err != nil {
		log.Errorf("Error sending data from OnStatePosted: %s", err)
		RespondWithError(c.RequestCtx, 500, "Error saving state")
		return
	}

	RespondEmpty(c.RequestCtx, 200)
}

func (i *Action) OnActorStatePosted(c *routing.Context) {
	targetID := c.Param(actionIDHeader)
	contextID := c.Param(contextIDPath)
	actorState := string(c.PostBody())

	key := fmt.Sprintf("%s-%s", targetID, contextID)
	err := i.SaveActorStateToStore(KeyValState{
		Key:   key,
		Value: actorState,
	})
	if err != nil {
		log.Errorf("Error from OnActorStatePosted: %s", err)
		RespondWithError(c.RequestCtx, 500, "Error saving state")
		return
	}
}

func (i *Action) GetStateKey(key string) string {
	return fmt.Sprintf("%s-%s", i.ActionID, key)
}

func (i *Action) GetState(ctx context.Context, in *pb.GetStateEnvelope) (*any.Any, error) {
	actionKey := i.GetStateKey(in.Key)
	state, err := i.GetStateFromStore(actionKey)
	if err != nil {
		log.Errorf("Error getting state: %s", err)
		return nil, err
	}

	if state == nil {
		return &any.Any{}, nil
	}

	return &any.Any{
		Value: []byte(state.(string)),
	}, nil
}

func (i *Action) OnActorGetState(c *routing.Context) {
	id := c.Param(actionIDHeader)
	contextID := c.Param(contextIDPath)
	key := fmt.Sprintf("%s-%s", id, contextID)

	state, err := i.GetStateFromStore(key)
	if err != nil {
		log.Errorf("Error getting actor state: %s", err)
		RespondWithError(c.RequestCtx, 500, "Error getting actor state")
		return
	}

	if state == nil {
		RespondEmpty(c.RequestCtx, 200)
	} else {
		RespondWithString(c.RequestCtx, 200, state.(string))
	}
}

func (i *Action) OnGetState(c *routing.Context) {
	key := c.Param("key")
	actionKey := i.GetStateKey(key)
	state, err := i.GetStateFromStore(actionKey)
	if err != nil {
		log.Errorf("Error getting state: %s", err)
		RespondWithError(c.RequestCtx, 500, "Error getting state")
		return
	}

	if state == nil {
		RespondEmpty(c.RequestCtx, 200)
	} else {
		RespondWithString(c.RequestCtx, 200, state.(string))
	}
}

func SerializeToJSON(obj interface{}) ([]byte, error) {
	buffer := &bytes.Buffer{}
	encoder := jsoniter.ConfigFastest.NewEncoder(buffer)
	encoder.SetEscapeHTML(false)
	err := encoder.Encode(obj)
	if err != nil {
		return nil, err
	}

	bytes := buffer.Bytes()
	return bytes, nil
}

func RespondWithJSON(ctx *fasthttp.RequestCtx, code int, obj []byte) {
	ctx.Response.Header.SetContentType("application/json")
	ctx.Response.SetStatusCode(code)
	ctx.Response.SetBody(obj)
}

func RespondWithString(ctx *fasthttp.RequestCtx, code int, obj string) {
	ctx.Response.Header.SetContentType("application/json")
	ctx.Response.SetStatusCode(code)
	ctx.Response.SetBodyString(obj)
}

func RespondWithError(ctx *fasthttp.RequestCtx, code int, message string) {
	json, _ := SerializeToJSON(map[string]string{"error": message})
	RespondWithJSON(ctx, code, json)
}

func RespondEmpty(ctx *fasthttp.RequestCtx, code int) {
	ctx.Response.SetBody(nil)
	ctx.Response.SetStatusCode(code)
}

func (i *Action) TraceSpanFromContext(c context.Context, events []Event, operation string) (context.Context, *trace.Span, *trace.SpanContext) {
	if i.Configuration.Spec.TracingSpec.Enabled {
		if i.Configuration.Spec.TracingSpec.IncludeEvent {
			return diag.TraceSpanFromContext(c, projectEvents(events), operation, i.Configuration.Spec.TracingSpec.IncludeEvent, i.Configuration.Spec.TracingSpec.IncludeEventBody)
		} else {
			return diag.TraceSpanFromContext(c, nil, operation, i.Configuration.Spec.TracingSpec.IncludeEvent, i.Configuration.Spec.TracingSpec.IncludeEventBody)
		}
	} else {
		return nil, nil, nil
	}
}
func (i *Action) TraceSpanFromRoutingContext(c *routing.Context, events []Event, operation string) (context.Context, *trace.Span, *trace.SpanContext) {
	if i.Configuration.Spec.TracingSpec.Enabled {
		if i.Configuration.Spec.TracingSpec.IncludeEvent {
			return diag.TraceSpanFromRoutingContext(c, projectEvents(events), operation, i.Configuration.Spec.TracingSpec.IncludeEvent, i.Configuration.Spec.TracingSpec.IncludeEventBody)
		} else {
			return diag.TraceSpanFromRoutingContext(c, nil, operation, i.Configuration.Spec.TracingSpec.IncludeEvent, i.Configuration.Spec.TracingSpec.IncludeEventBody)
		}
	} else {
		return nil, nil, nil
	}
}
func projectEvents(events []Event) *[]diag.Event {
	bytes, _ := json.Marshal(events)
	var ret []diag.Event = make([]diag.Event, len(events))
	json.Unmarshal(bytes, &ret)
	return &ret
}
func (i *Action) InitRoutes() *routing.Router {
	router := routing.New()

	router.Post("/publish", func(c *routing.Context) error {
		i.OnEventPublished(c)
		return nil
	})

	router.Get("/action/<id>/<method>", func(c *routing.Context) error {
		i.OnInvokeAction(c)
		return nil
	})

	router.Post("/action/<id>/<method>", func(c *routing.Context) error {
		i.OnInvokeAction(c)
		return nil
	})

	router.Delete("/action/<id>/<method>", func(c *routing.Context) error {
		i.OnInvokeAction(c)
		return nil
	})

	router.Put("/action/<id>/<method>", func(c *routing.Context) error {
		i.OnInvokeAction(c)
		return nil
	})

	router.Post("/action/<id>/activate/local", func(c *routing.Context) error {
		i.OnLocalBatchActivation(c)
		return nil
	})

	router.Get("/action/<id>/<contextid>/<method>", func(c *routing.Context) error {
		i.OnInvokeAction(c)
		return nil
	})

	router.Post("/action/<id>/<contextid>/<method>", func(c *routing.Context) error {
		i.OnInvokeAction(c)
		return nil
	})

	router.Delete("/action/<id>/<contextid>/<method>", func(c *routing.Context) error {
		i.OnInvokeAction(c)
		return nil
	})

	router.Put("/action/<id>/<contextid>/<method>", func(c *routing.Context) error {
		i.OnInvokeAction(c)
		return nil
	})

	router.Post("/state", func(c *routing.Context) error {
		i.OnStatePosted(c)
		return nil
	})

	router.Get("/state/<key>", func(c *routing.Context) error {
		i.OnGetState(c)
		return nil
	})

	router.Get("/state/<id>/<contextid>", func(c *routing.Context) error {
		i.OnActorGetState(c)
		return nil
	})

	router.Post("/state/<id>/<contextid>", func(c *routing.Context) error {
		i.OnActorStatePosted(c)
		return nil
	})

	router.Put("/state/<id>/<contextid>", func(c *routing.Context) error {
		i.OnActorStatePosted(c)
		return nil
	})

	router.Post("/invoke/*", func(c *routing.Context) error {
		i.OnActionInvoked(c)
		return nil
	})

	router.Get("/metadata", func(c *routing.Context) error {
		i.OnGetMetadata(c)
		return nil
	})

	return router
}

func (i *Action) OnGetMetadata(c *routing.Context) {
	m := ActionMetadata{
		ID:         i.ActionID,
		AppAddress: i.GetLocalAppEndpoint(""),
		Protocol:   string(i.Protocol),
		Actors:     []ActorMetadata{},
		Healthy:    true,
	}

	if i.StateStore != nil {
		for _, es := range i.EventSources {
			if es.Name == string(stateStoreType) {
				m.StateStore = es.Spec.Type
				break
			}
		}
	}

	if len(i.AppConfig.Entities) > 0 {
		for _, e := range i.AppConfig.Entities {
			am := ActorMetadata{
				ActorType:         e,
				ActivatedContexts: []string{},
			}

			for k, v := range i.ActiveContexts {
				if v == e {
					am.ActivatedContexts = append(am.ActivatedContexts, k)
				}
			}

			m.Actors = append(m.Actors, am)
		}
	}

	b, err := i.json.Marshal(&m)
	if err != nil {
		log.Errorf("Error getting metadata: %s", err)
		RespondWithError(c.RequestCtx, 500, "Error getting metadata")
		return
	}

	RespondWithJSON(c.RequestCtx, 200, b)
}

func (i *Action) OnActionInvoked(c *routing.Context) {

	_, span, spanCtx := i.TraceSpanFromRoutingContext(c, nil, "OnActionInvoked")
	if span != nil {
		defer span.End()
	}

	actionMethod := string(c.Path())[8:]
	verbMethod := string(c.Method())
	contextID := string(c.Request.Header.Peek(contextIDHeader))
	from := string(c.Request.Header.Peek(fromHeader))
	targetID := string(c.Request.Header.Peek(actionTargetIDHeader))
	payload := c.RequestCtx.PostBody()

	headers := map[string]string{
		fromHeader:           from,
		actionTargetIDHeader: targetID,
	}
	if contextID != "" {
		headers[contextIDHeader] = contextID
	}

	var url string
	if contextID != "" {
		verbMethod = http.MethodPut
		url = i.GetLocalAppEndpoint(fmt.Sprintf("actors/%s/%s/%s", targetID, contextID, actionMethod))
	} else {
		url = i.GetLocalAppEndpoint(actionMethod)
	}

	r, err := i.DispatchInvokeToApp(url, actionMethod, verbMethod, headers, payload, spanCtx)
	if err != nil {
		RespondWithError(c.RequestCtx, 500, err.Error())
		return
	}

	RespondWithJSON(c.RequestCtx, 200, r)
}

func (i *Action) invoke(actionMethod, targetID, contextID, fromID, verbMethod string, queryString string, payload []byte, ctx *trace.SpanContext) ([]byte, error) {
	headers := map[string]string{
		fromHeader:           fromID,
		actionTargetIDHeader: targetID,
	}

	if contextID != "" {
		headers[contextIDHeader] = contextID
	}

	var url string
	if contextID != "" {
		activated := i.ContextActivated(targetID, contextID)
		if !activated {
			err := i.ActivateContext(targetID, contextID)
			if err != nil {
				log.Errorf("Error activating %s/%s: %s", targetID, contextID, err)
			}
		}

		verbMethod = http.MethodPut
		url = i.GetLocalAppEndpoint(fmt.Sprintf("actors/%s/%s/%s", targetID, contextID, actionMethod))
	} else {
		url = i.GetLocalAppEndpoint(actionMethod)
	}
	if queryString != "" {
		url = url + "?" + queryString
	}

	r, err := i.DispatchInvokeToApp(url, actionMethod, verbMethod, headers, payload, ctx)
	if err != nil {
		return nil, err
	}

	return r, nil
}

func (i *Action) Invoke(ctx context.Context, in *pb.InvokeEnvelope) (*pb.InvokeEnvelope, error) {

	md, _ := metadata.FromIncomingContext(ctx)
	actionMethod := md.Get(invokedMethodHeader)[0]
	targetID := md.Get(actionTargetIDHeader)[0]
	contextIDMetadata := md.Get(contextIDHeader)
	from := md.Get(fromHeader)[0]
	verbMethod := md.Get(invokedHTTPVerbHeader)[0]

	corId := md.Get(diag.CorrelationID)[0]
	_, tSpan := diag.TraceSpanFromCorrelationId(corId, "Invoke", actionMethod, targetID, from, verbMethod)
	if tSpan != nil {
		defer tSpan.End()
	}

	payload := in.GetData().GetValue()
	contextID := ""
	if len(contextIDMetadata) > 0 {
		contextID = contextIDMetadata[0]
	}

	spanContext := diag.DeserializeSpanContextPointer(corId)

	r, err := i.invoke(actionMethod, targetID, contextID, from, verbMethod, "", payload, spanContext)
	if err != nil {
		diag.SetSpanStatus(tSpan, trace.StatusCodeInternal, err.Error())
		return nil, err
	}

	diag.SetSpanStatus(tSpan, trace.StatusCodeOK, fmt.Sprintf("action invocation succeeded with return value %s", string(r)))
	return &pb.InvokeEnvelope{
		Data: &any.Any{
			Value: r,
		},
	}, nil
}

func (i *Action) SendStateViaGRPC(url string, state []KeyValState) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute*1)
	defer cancel()

	s := &pb.State{}
	for _, item := range state {
		b, err := i.json.Marshal(item.Value)
		if err != nil {
			continue
		}

		s.State = append(s.State, &pb.KeyVal{
			Key: item.Key,
			Value: &any.Any{
				Value: b,
			},
		})
	}

	c := pb.NewAppClient(i.GRPCClient)
	_, err := c.RestoreState(ctx, s)
	if err != nil {
		return err
	}

	return nil
}

func (i *Action) ExecuteGRPCInvokeCall(url, appMethod string, headers map[string]string, payload []byte) ([]byte, error) {
	md := metadata.New(headers)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute*1)
	defer cancel()

	contextID := ""
	if val, ok := headers[contextIDHeader]; ok {
		contextID = val
	}

	ctxMetadata := metadata.NewOutgoingContext(ctx, md)
	c := pb.NewAppClient(i.GRPCClient)
	resp, err := c.Invoke(ctxMetadata, &pb.InvokeMethod{
		MethodName: appMethod,
		ContextId:  contextID,
		Data:       &any.Any{Value: payload},
	})
	if err != nil {
		return nil, err
	}

	return resp.Value, nil
}

func (i *Action) DispatchInvokeToApp(url, appMethod, verbMethod string, headers map[string]string, payload []byte, ctx *trace.SpanContext) ([]byte, error) {
	if i.Protocol == GRPCProtocol {
		r, err := i.ExecuteGRPCInvokeCall(url, appMethod, headers, payload)
		if err != nil {
			return nil, err
		}

		return r, nil
	}

	r, err := i.ExecuteHTTPCall(url, verbMethod, headers, payload, ctx)
	if err != nil {
		return nil, err
	}

	return r, nil
}

func (i *Action) UpdateEventSource(ctx context.Context, in *pb.EventSource) (*empty.Empty, error) {
	e := EventSource{
		Name: in.Name,
		Spec: EventSourceSpec{
			Type: in.Spec.Type,
		},
	}

	var connectionInfo interface{}
	err := i.json.Unmarshal(in.Spec.ConnectionInfo.Value, &connectionInfo)
	if err != nil {
		log.Errorf("Error updating event source: %s", err)
	} else {
		e.Spec.ConnectionInfo = connectionInfo
		updated := false
		for index, es := range i.EventSources {
			if es.Name == e.Name {
				i.EventSources[index] = e
				updated = true
				break
			}
		}
		if !updated {
			i.EventSources = append(i.EventSources, e)
		}
	}

	return &empty.Empty{}, nil
}

func (i *Action) SetupStateStore() {
	for _, es := range i.EventSources {
		if es.Name == string(stateStoreType) {
			stateStore := i.GetActionSource(es.Spec.Type)
			err := stateStore.Init(es.Spec)
			if err != nil {
				log.Errorf("Error from state store Init: %s", err)
				return
			}

			if stateStore != nil {
				i.StateStore = stateStore
				log.Infof("State Store of type %s found and initialized", es.Spec.Type)
				break
			}
		}
	}
}

func (i *Action) RegisterActionSources() {
	i.ActionSources = make(map[string]ActionSource)
	i.ActionSources["azure.messaging.eventhubs"] = NewAzureEventHubs()
	i.ActionSources["aws.messaging.sns"] = NewAWSSns()
	i.ActionSources["aws.messaging.sqs"] = NewAWSSQS()
	i.ActionSources["gcp.storage.bucket"] = NewGCPStorage()
	i.ActionSources["actions.state.local"] = NewMemoryStateStore()
	i.ActionSources["actions.state.redis"] = NewRedisStateStore(i.json)
	i.ActionSources["actions.state.cosmosdb"] = NewCosmosDBStateStore()
	i.ActionSources["actions.test.mock"] = NewMockEventSource()
	i.ActionSources["actions.sender.redis"] = NewRedisSender()
	i.ActionSources["http"] = NewHttpSource()
	i.ActionSources["azure.databases.cosmosdb"] = NewCosmosDB()
	i.ActionSources["aws.databases.dynamodb"] = NewDynamoDB()
	i.ActionSources["kafka"] = NewKafka()
	i.ActionSources["redis"] = NewRedis()
	i.ActionSources["mqtt"] = NewMQTT()
	i.ActionSources["rabbitmq"] = NewRabbitMQ()
}

func (i *Action) WaitUntilAppIsReady() {
	if i.ApplicationPort == "" {
		return
	}

	log.Infof("Application protocol: %s", string(i.Protocol))
	log.Infof("Waiting for app on port %s", i.ApplicationPort)

	for {
		conn, _ := net.DialTimeout("tcp", net.JoinHostPort("localhost", i.ApplicationPort), time.Millisecond*500)
		if conn != nil {
			conn.Close()
			break
		}
	}

	log.Infof("Application discovered on port %s", i.ApplicationPort)
}

func (i *Action) CreateAppChannels() {
	if i.ApplicationPort != "" && i.Protocol == GRPCProtocol {
		c, err := i.GetGRPCConnection(fmt.Sprintf("127.0.0.1:%s", i.ApplicationPort))
		if err != nil {
			log.Errorf("Error establishing connection to app grpc on port %s: %s", i.ApplicationPort, err)
			return
		}

		i.GRPCClient = c
	}
}

func (i *Action) Run(httpPort int, grpcPort int) {
	start := time.Now()
	log.Infof("Action running in %s mode", i.Mode)

	i.ConfigureTracing()
	i.WaitUntilAppIsReady()
	i.CreateAppChannels()
	i.GetApplicationConfig()
	i.RegisterActionSources()
	i.SetupEventSources()
	i.SetupSender()
	i.SetupStateStore()
	i.TryRestoreState()
	i.StartHTTPServer(httpPort)
	i.StartGRPCServer(grpcPort)
	i.SetIPAddress()

	d := time.Since(start).Seconds() * 1000
	log.Infof("Action initialized. Status: Running. Init Elapsed %vms", d)

	go i.ConnectToAssignerService()
}

func (i *Action) SetupSender() {
	for _, es := range i.EventSources {
		if es.Name == sender {
			sender := i.GetActionSource(es.Spec.Type)
			err := sender.Init(es.Spec)
			if err != nil {
				log.Errorf("Error from sender Init - %s", err)
				return
			}

			if sender != nil {
				i.Sender = sender.(Sender)
				i.Sender.StartLoop(i.PostEventToAppWithRetries, context.Background())
				log.Infof("Sender of type %s found and initialized", es.Spec.Type)
				break
			}
		}
	}
}

func (i *Action) StartHTTPServer(httpPort int) {
	log.Info("Starting Action HTTP server")

	go func() {
		router := i.InitRoutes()
		withCors := cors.NewCorsHandler(cors.Options{
			AllowedOrigins: i.AllowedOrigins,
			Debug:          false,
		})
		log.Fatal(fasthttp.ListenAndServe(fmt.Sprintf(":%v", httpPort), withCors.CorsMiddleware(router.HandleRequest)))
	}()
}

func (i *Action) TryRestoreState() {
	if i.StateStore != nil && i.ApplicationPort != "" {

		_, span, spanCtx := i.TraceSpanFromContext(context.Background(), nil, "TryRestoreState")
		if span != nil {
			defer span.End()
		}

		s := i.StateStore.(StateStore)
		state, err := s.GetAll(i.ActionID)
		if err == nil && len(state) > 0 {
			url := i.GetLocalAppEndpoint("state")

			if i.Protocol == GRPCProtocol {
				err = i.SendStateViaGRPC(url, state)
			} else if i.Protocol == HTTPProtocol {
				err = i.SendStateViaHTTP(url, state, spanCtx)
			}

			if err != nil {
				log.Errorf("Error restoring state to app: %s", err)
			} else {
				log.Info("State restored successfully")
			}
		}
	}
}

func (i *Action) SetIPAddress() {
	if i.Mode == string(StandaloneMode) {
		addrs, err := net.InterfaceAddrs()
		if err != nil {
			log.Errorf("Error getting interfaces: %s", err)
			return
		}

		for _, a := range addrs {
			if ipnet, ok := a.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
				if ipnet.IP.To4() != nil {
					i.IPAddress = ipnet.IP.String()
					return
				}
			}
		}
	} else if i.Mode == string(KubernetesMode) {
		i.IPAddress = os.Getenv("HOST_IP")
	}
}

func (i *Action) GetApplicationConfig() {
	if i.Protocol == GRPCProtocol {
		client := pb.NewAppClient(i.GRPCClient)
		resp, err := client.GetConfig(context.Background(), &empty.Empty{})
		if err != nil {
			return
		}

		if resp != nil {
			i.AppConfig = ApplicationConfig{Entities: resp.Entities}
			log.Info("Application config pulled successfully")
		}
	} else if i.Protocol == HTTPProtocol {
		url := i.GetLocalAppEndpoint("actions/config")
		resp, err := i.ExecuteHTTPCall(url, "GET", nil, nil, nil)
		if err != nil {
			return
		}

		var config ApplicationConfig
		err = i.json.Unmarshal(resp, &config)
		if err != nil {
			return
		}

		i.AppConfig = config
		log.Info("Application config pulled successfully")
	}
}

func (i *Action) ContextActivated(targetID, contextID string) bool {
	i.ActiveContextsLock.RLock()
	defer i.ActiveContextsLock.RUnlock()
	_, exists := i.ActiveContexts[contextID]
	return exists
}

func (i *Action) ActivateContext(targetID, contextID string) error {
	i.ActiveContextsLock.Lock()
	defer i.ActiveContextsLock.Unlock()

	contextKey := fmt.Sprintf("%s-%s", targetID, contextID)
	state, err := i.GetStateFromStore(contextKey)
	if err != nil {
		return err
	}

	payload := "{}"
	if state != nil && state.(string) != "" {
		payload = state.(string)
	}

	url := i.GetLocalAppEndpoint(fmt.Sprintf("actors/%s/%s", targetID, contextID))
	_, err = i.ExecuteHTTPCall(url, http.MethodPost, nil, []byte(payload), nil)
	if err != nil {
		return err
	}

	i.ActiveContexts[contextID] = targetID
	return nil
}

func (i *Action) GetAssignerClientPersistently() pb.Assigner_ReportActionStatusClient {
	for {
		conn, err := grpc.Dial(i.AssignerAddress, grpc.WithInsecure())
		if err != nil {
			time.Sleep(time.Second * 1)
			continue
		}
		header := metadata.New(map[string]string{"id": i.IPAddress})
		ctx := metadata.NewOutgoingContext(context.Background(), header)
		client := pb.NewAssignerClient(conn)
		stream, err := client.ReportActionStatus(ctx)
		if err != nil {
			time.Sleep(time.Second * 1)
			continue
		}

		return stream
	}
}

func (i *Action) ConnectToAssignerService() {
	if i.AssignerAddress == "" {
		log.Info("No assignment service discovered")
		return
	}

	log.Infof("Starting connection attempt to assigner service at %s", i.AssignerAddress)
	stream := i.GetAssignerClientPersistently()

	log.Infof("Established connection to assigner service at %s", i.AssignerAddress)

	go func() {
		for {
			host := pb.Host{
				Name:     i.IPAddress,
				Load:     1,
				Entities: i.AppConfig.Entities,
				Port:     int64(gRPCPort),
			}

			if stream != nil {
				if err := stream.Send(&host); err != nil {
					log.Error("Connection failure to assigner: retrying")
					stream = i.GetAssignerClientPersistently()
				}
			}
			time.Sleep(time.Second * 1)
		}
	}()

	go func() {
		for {
			resp, err := stream.Recv()
			if err != nil {
				log.Error("Connection failure to assigner: retrying")
				stream = i.GetAssignerClientPersistently()
			}
			if resp != nil {
				i.OnAssignmentOrder(resp)
			}
		}
	}()
}

func (i *Action) LookupContextAddress(actionID, contextID string) string {
	i.AssignmentTableLock.RLock()
	defer i.AssignmentTableLock.RUnlock()

	t := i.AssignmentTables.Entries[actionID]
	if t == nil {
		return ""
	}
	a, _ := t.GetHost(contextID)
	return fmt.Sprintf("%s:%v", a.Name, a.Port)
}

func (i *Action) BlockAssignments() {
	i.AssignmentSignal = make(chan struct{})
	i.AssignmentBlock = true
}

func (i *Action) UnblockAssignments() {
	if i.AssignmentBlock {
		i.AssignmentBlock = false
		close(i.AssignmentSignal)
	}
}

func (i *Action) OnAssignmentOrder(in *pb.AssignerOrder) {
	log.Infof("Assignment order received: %s", in.Operation)

	switch in.Operation {
	case "lock":
		{
			i.BlockAssignments()

			go func() {
				time.Sleep(time.Second * 5)
				i.UnblockAssignments()
			}()
		}
	case "unlock":
		{
			i.UnblockAssignments()
		}
	case "update":
		{
			i.UpdateAssignments(in.Tables)
		}
	}
}

func (i *Action) UpdateAssignments(in *pb.AssignmentTables) {
	if in.Version != i.AssignmentTables.Version {
		i.AssignmentTableLock.Lock()
		defer i.AssignmentTableLock.Unlock()

		for k, v := range in.Entries {
			loadMap := map[string]*consistenthash.Host{}
			for lk, lv := range v.LoadMap {
				loadMap[lk] = consistenthash.NewHost(lv.Name, lv.Load, lv.Port)
			}
			c := consistenthash.NewFromExisting(v.Hosts, v.SortedSet, loadMap)
			i.AssignmentTables.Entries[k] = c
		}

		i.AssignmentTables.Version = in.Version

		log.Info("Assignment tables updated")
	}
}
