/*
Copyright 2022 The Dapr Authors
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

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/PaesslerAG/jsonpath"
	"github.com/google/uuid"
	"github.com/gorilla/mux"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/zipkin"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdk_trace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.4.0"
	"go.opentelemetry.io/otel/trace"

	"github.com/dapr/dapr/tests/apps/utils"
)

var (
	appPort      = 3000
	daprHTTPPort = 3500

	httpClient = utils.NewHTTPClient()

	zipkinEndpoint = "http://dapr-zipkin:9411"
	serviceName    = "tracingapp"

	tracer      trace.Tracer
	propagators propagation.TextMapPropagator
)

const (
	jsonContentType = "application/json"
	zipkinSpans     = "/api/v2/spans"
	zipkinTraces    = "/api/v2/traces"

	expectedDaprChildSpanNameTemplate = "calllocal/%s/invoke/something"
)

func init() {
	p := os.Getenv("DAPR_HTTP_PORT")
	if p != "" && p != "0" {
		daprHTTPPort, _ = strconv.Atoi(p)
	}
	p = os.Getenv("PORT")
	if p != "" && p != "0" {
		appPort, _ = strconv.Atoi(p)
	}
	p = os.Getenv("ZIPKIN_ENDPOINT")
	if p != "" {
		zipkinEndpoint = p
	}
	p = os.Getenv("SERVICE_NAME")
	if p != "" {
		serviceName = p
	}
}

type appResponse struct {
	SpanName *string `json:"spanName,omitempty"`
	Message  string  `json:"message,omitempty"`
}

// indexHandler is the handler for root path
func indexHandler(w http.ResponseWriter, r *http.Request) {
	log.Println("indexHandler is called")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(appResponse{Message: "OK"})
}

// triggerInvoke is the handler for end-to-end to start the invoke stack
func triggerInvoke(w http.ResponseWriter, r *http.Request) {
	log.Printf("Processing %s %s", r.Method, r.URL.RequestURI())
	uuidObj, _ := uuid.NewRandom()
	uuid := uuidObj.String()
	ctx := propagators.Extract(r.Context(), propagation.HeaderCarrier(r.Header))
	newCtx, span := tracer.Start(ctx, uuid)
	defer span.End()

	query := r.URL.Query()
	appID := query.Get("appId")

	url := fmt.Sprintf("http://127.0.0.1:%d/v1.0/invoke/%s/method/invoke/something", daprHTTPPort, appID)
	/* #nosec */
	req, _ := http.NewRequest(http.MethodPost, url, nil)
	req = req.WithContext(newCtx)
	hc := propagation.HeaderCarrier(req.Header)
	propagators.Inject(newCtx, hc)
	req.Header.Add("Content-Type", jsonContentType)

	log.Printf("span's name is %s and invoke url is %s\n", uuid, url)

	res, err := httpClient.Do(req)
	if res != nil {
		defer res.Body.Close()
	}

	if err != nil {
		log.Println(err)

		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(appResponse{
			SpanName: &uuid,
			Message:  err.Error(),
		})
		return
	}

	if res.StatusCode != http.StatusOK {
		w.WriteHeader(http.StatusExpectationFailed)
		json.NewEncoder(w).Encode(appResponse{
			SpanName: &uuid,
			Message:  fmt.Sprintf("expected status code %d, got %d", http.StatusOK, res.StatusCode),
		})
	}

	json.NewEncoder(w).Encode(appResponse{
		SpanName: &uuid,
		Message:  "OK",
	})
}

// invoke is the handler for end-to-end to invoke
func invoke(w http.ResponseWriter, r *http.Request) {
	log.Printf("Processing %s %s", r.Method, r.URL.RequestURI())
	ctx := propagators.Extract(r.Context(), propagation.HeaderCarrier(r.Header))
	_, span := tracer.Start(ctx, "invokedMethod")
	defer span.End()

	// Pretend to do work
	time.Sleep(time.Second)

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(appResponse{Message: "OK"})
}

// validate is the handler to validate the tracing span
func validate(w http.ResponseWriter, r *http.Request) {
	err := doValidate(w, r)
	if err != nil {
		log.Println(err)

		w.WriteHeader(http.StatusExpectationFailed)
		json.NewEncoder(w).Encode(appResponse{Message: err.Error()})
	}

	json.NewEncoder(w).Encode(appResponse{Message: "OK"})
}

func doValidate(w http.ResponseWriter, r *http.Request) error {
	query := r.URL.Query()
	mainSpanName := query.Get("spanName")
	resp, err := http.Get(zipkinEndpoint + zipkinTraces)
	if err != nil {
		return err
	}

	defer resp.Body.Close()

	v := interface{}(nil)

	json.NewDecoder(resp.Body).Decode(&v)

	mainSpanID, err := findUniqueValueFromJSONPath("$..[?(@.name==\""+mainSpanName+"\")].id", v)
	if err != nil {
		return err
	}

	if mainSpanID == "" {
		return errors.New("empty span id found for span name " + mainSpanName)
	}
	log.Printf("Found main span with name %s and id=%s", mainSpanName, mainSpanID)

	childSpanName, err := findUniqueValueFromJSONPath("$..[?(@.parentId==\""+mainSpanID+"\")].name", v)
	if err != nil {
		return err
	}

	remoteServiceName, err := findUniqueValueFromJSONPath("$..[?(@.parentId==\""+mainSpanID+"\")].remoteEndpoint.serviceName", v)
	if err != nil {
		return err
	}

	expectedDaprChildSpanName := fmt.Sprintf(expectedDaprChildSpanNameTemplate, remoteServiceName)
	if childSpanName != expectedDaprChildSpanName {
		return errors.New("child span name is not correct, expected " + expectedDaprChildSpanName + ", actual " + childSpanName)
	}

	log.Printf("Tracing is correct for span with name=%s", mainSpanName)
	return nil
}

func findUniqueValueFromJSONPath(jsonPath string, v interface{}) (string, error) {
	values, err := jsonpath.Get(jsonPath, v)
	if err != nil {
		return "", err
	}

	arrValues := values.([]interface{})
	log.Printf("%v", arrValues)

	if len(arrValues) == 0 {
		return "", errors.New("no value found for json path " + jsonPath)
	}

	if len(arrValues) > 1 {
		return "", errors.New("more than one value found for json path " + jsonPath)
	}

	return fmt.Sprintf("%v", arrValues[0]), nil
}

// appRouter initializes restful api router
func appRouter() http.Handler {
	router := mux.NewRouter().StrictSlash(true)

	// Log requests and their processing time
	router.Use(utils.LoggerMiddleware)

	router.HandleFunc("/", indexHandler).Methods("GET")
	router.HandleFunc("/triggerInvoke", triggerInvoke).Methods("POST")
	router.HandleFunc("/invoke/something", invoke).Methods("POST")
	router.HandleFunc("/validate", validate).Methods("POST", "GET")

	router.Use(mux.CORSMethodMiddleware(router))

	return router
}

func main() {
	exporter, err := zipkin.New(zipkinEndpoint + zipkinSpans)
	if err != nil {
		log.Fatalf("failed to create exporter: %v", err)
	}

	res := resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceNameKey.String(serviceName),
	)

	tp := sdk_trace.NewTracerProvider(
		sdk_trace.WithBatcher(exporter),
		sdk_trace.WithResource(res),
	)

	defer func() {
		if err := tp.Shutdown(context.Background()); err != nil {
			log.Fatal(err)
		}
	}()

	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})

	propagators = otel.GetTextMapPropagator()
	tracer = otel.Tracer(serviceName)

	log.Printf("Tracing App - listening on http://localhost:%d", appPort)
	utils.StartServer(appPort, appRouter, true, false)
}
