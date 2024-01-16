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

//nolint:forbidigo
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	guuid "github.com/google/uuid"
	"github.com/gorilla/mux"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/anypb"

	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/apps/utils"
)

var (
	appPort      = 3000
	daprHTTPPort = 3500
	daprGRPCPort = 50001

	httpClient = utils.NewHTTPClient()
	daprClient runtimev1pb.DaprClient

	httpMethods []string
	pid         string
)

const (
	jsonContentType = "application/json"
)

func init() {
	p := os.Getenv("DAPR_HTTP_PORT")
	if p != "" && p != "0" {
		daprHTTPPort, _ = strconv.Atoi(p)
	}
	p = os.Getenv("DAPR_GRPC_PORT")
	if p != "" && p != "0" {
		daprGRPCPort, _ = strconv.Atoi(p)
	}
	p = os.Getenv("PORT")
	if p != "" && p != "0" {
		appPort, _ = strconv.Atoi(p)
	}
}

type testCommandRequest struct {
	RemoteApp        string  `json:"remoteApp,omitempty"`
	Method           string  `json:"method,omitempty"`
	RemoteAppTracing string  `json:"remoteAppTracing"`
	Message          *string `json:"message,omitempty"`
}

type testCommandRequestExternal struct {
	testCommandRequest `json:",inline"`
	ExternalIP         string `json:"externalIP,omitempty"`
}

type appResponse struct {
	Message string `json:"message,omitempty"`
}

type negativeTestResult struct {
	MainCallSuccessful bool                   `json:"callSuccessful"`
	RawBody            []byte                 `json:"rawBody"`
	RawError           string                 `json:"rawError"`
	Results            []individualTestResult `json:"results"`
}

type individualTestResult struct {
	TestCase       string `json:"case"`
	CallSuccessful bool   `json:"callSuccessful"`
}

type httpTestMethods struct {
	Verb       string
	Callback   string
	SendBody   bool
	ExpectBody bool
}

var testMethods = []httpTestMethods{
	{
		Verb:       "GET",
		Callback:   "gethandler",
		SendBody:   false,
		ExpectBody: true,
	},
	{
		Verb:       "HEAD",
		Callback:   "headhandler",
		SendBody:   false,
		ExpectBody: false,
	},
	{
		Verb:       "POST",
		Callback:   "posthandler",
		SendBody:   true,
		ExpectBody: true,
	},
	{
		Verb:       "PUT",
		Callback:   "puthandler",
		SendBody:   true,
		ExpectBody: true,
	},
	{
		Verb:       "DELETE",
		Callback:   "deletehandler",
		SendBody:   true,
		ExpectBody: true,
	},
	// Go's net/http library does not support sending requests with the CONNECT method
	/*{
		Verb:       "CONNECT",
		Callback:   "connecthandler",
		SendBody:   true,
		ExpectBody: true,
	},*/
	{
		Verb:       "OPTIONS",
		Callback:   "optionshandler",
		SendBody:   true,
		ExpectBody: true,
	},
	{
		Verb:       "TRACE",
		Callback:   "tracehandler",
		SendBody:   true,
		ExpectBody: true,
	},
	{
		Verb:       "PATCH",
		Callback:   "patchhandler",
		SendBody:   true,
		ExpectBody: true,
	},
}

// indexHandler is the handler for root path
func indexHandler(w http.ResponseWriter, r *http.Request) {
	log.Println("indexHandler is called")

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(appResponse{Message: "OK"})
}

func pidHandler(w http.ResponseWriter, r *http.Request) {
	log.Println("pidHandler is called")

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(appResponse{Message: pid})
}

func singlehopHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	response := "singlehop is called"
	json.NewEncoder(w).Encode(appResponse{Message: response})
}

func multihopHandler(w http.ResponseWriter, r *http.Request) {
	response, statusCode, err := invokeService("serviceinvocation-callee-1", "singlehop")
	if err != nil {
		onHTTPCallFailed(w, statusCode, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

// Handles a request with a JSON body.  Extracts s string from the input json and returns in it an appResponse.
func withBodyHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("withBodyHandler called. HTTP Verb: %s\n", r.Method)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		onBadRequest(w, err)
		return
	}

	if len(body) > 100 {
		log.Printf("withBodyHandler body (first 100 bytes): %s\n", string(body[:100]))
	} else {
		log.Printf("withBodyHandler body: %s\n", string(body))
	}
	var s string
	err = json.Unmarshal(body, &s)
	if err != nil {
		onDeserializationFailed(w, err)
		return
	}

	w.Header().Add("x-dapr-tests-request-method", r.Method)
	w.WriteHeader(http.StatusOK)

	json.NewEncoder(w).Encode(appResponse{Message: s})
}

// Handles a request with no body.  Returns an appResponse with appResponse.Message "ok", which caller validates.
func noBodyHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Printf("noBodyHandler called. HTTP Verb: %s \n", r.Method)
	w.Header().Add("x-dapr-tests-request-method", r.Method)

	logAndSetResponse(w, http.StatusOK, "ok")
}

func opAllowHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	response := "opAllow is called"
	json.NewEncoder(w).Encode(appResponse{Message: response})
}

func opDenyHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	response := "opDeny is called"
	json.NewEncoder(w).Encode(appResponse{Message: response})
}

func opRedirectHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Add("Location", "http://localhost:3500/v1.0/invoke/serviceinvocation-callee-1/method/opAllow")
	w.WriteHeader(http.StatusTemporaryRedirect)
	response := "opRedirect is called"
	json.NewEncoder(w).Encode(appResponse{Message: response})
}

func testHandler(w http.ResponseWriter, r *http.Request) {
	log.Println("Enter testHandler")
	var commandBody testCommandRequest
	err := json.NewDecoder(r.Body).Decode(&commandBody)
	if err != nil {
		onBadRequest(w, err)
		return
	}

	log.Printf("  testHandler invoking %s with method %s\n", commandBody.RemoteApp, commandBody.Method)
	response, statusCode, err := invokeService(commandBody.RemoteApp, commandBody.Method)
	if err != nil {
		w.WriteHeader(statusCode)
		json.NewEncoder(w).Encode(appResponse{
			Message: err.Error(),
		})
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

func invokeService(remoteApp, method string) (appResponse, int, error) {
	return invokeServiceWithBody(remoteApp, method, nil)
}

func invokeServiceWithBody(remoteApp, method string, data []byte) (appResponse, int, error) {
	resp, err := invokeServiceWithBodyHeader(remoteApp, method, data, nil)
	var statusCode int
	if resp != nil {
		statusCode = resp.StatusCode
	}
	if err != nil {
		return appResponse{}, statusCode, err
	}
	defer resp.Body.Close()

	var appResp appResponse
	err = json.NewDecoder(resp.Body).Decode(&appResp)
	if err != nil {
		return appResponse{}, statusCode, err
	}

	// invokeServiceWithBodyHeader uses http client.Do method which
	// returns success for everything except 2xx error codes. Check
	// the status code to extract non 2xx errors.
	if statusCode != http.StatusOK {
		return appResponse{}, statusCode, errors.New(appResp.Message)
	}

	return appResp, statusCode, nil
}

func invokeServiceWithBodyHeader(remoteApp, method string, data []byte, headers http.Header) (*http.Response, error) {
	url := fmt.Sprintf("http://localhost:%s/v1.0/invoke/%s/method/%s", strconv.Itoa(daprHTTPPort), remoteApp, method)
	log.Printf("invoke url is %s", url)

	var t io.Reader
	if data != nil {
		t = bytes.NewReader(data)
	}

	/* #nosec */
	req, _ := http.NewRequest(http.MethodPost, url, t)
	for k, v := range headers {
		req.Header[k] = v
	}

	req.Header.Add("Content-Type", jsonContentType)
	return httpClient.Do(req)
}

func invokeServiceWithDaprAppIDHeader(remoteApp, method string, data []byte, headers http.Header) (*http.Response, error) {
	url := fmt.Sprintf("http://localhost:%s/%s", strconv.Itoa(daprHTTPPort), method)
	fmt.Printf("invoke url is %s\n", url)

	var t io.Reader
	if data != nil {
		t = bytes.NewReader(data)
	}

	/* #nosec */
	req, _ := http.NewRequest(http.MethodPost, url, t)
	req.Header.Set("dapr-app-id", remoteApp)
	for k, v := range headers {
		req.Header[k] = v
	}

	req.Header.Set("Content-Type", jsonContentType)
	return httpClient.Do(req)
}

func constructRequest(id, method, httpVerb string, body []byte) *runtimev1pb.InvokeServiceRequest {
	msg := &commonv1pb.InvokeRequest{Method: method}
	msg.ContentType = jsonContentType
	msg.Data = &anypb.Any{Value: body}
	if httpVerb != "" {
		msg.HttpExtension = &commonv1pb.HTTPExtension{
			Verb: commonv1pb.HTTPExtension_Verb(commonv1pb.HTTPExtension_Verb_value[httpVerb]), //nolint:nosnakecase
		}
	}

	return &runtimev1pb.InvokeServiceRequest{
		Id:      id,
		Message: msg,
	}
}

// appRouter initializes restful api router
func appRouter() http.Handler {
	router := mux.NewRouter().StrictSlash(true).UseEncodedPath()

	// Log requests and their processing time
	router.Use(utils.LoggerMiddleware)

	router.HandleFunc("/", indexHandler).Methods("GET")
	router.HandleFunc("/pid", pidHandler).Methods("POST")
	router.HandleFunc("/singlehop", singlehopHandler).Methods("POST")
	router.HandleFunc("/multihop", multihopHandler).Methods("POST")

	router.HandleFunc("/opAllow", opAllowHandler).Methods("POST")
	router.HandleFunc("/opDeny", opDenyHandler).Methods("POST")

	router.PathPrefix("/opRedirect").Handler(http.HandlerFunc(opRedirectHandler)).Methods("POST")

	router.HandleFunc("/tests/invoke_test", testHandler)

	// these are called through dapr service invocation
	for _, test := range testMethods {
		if test.SendBody {
			router.HandleFunc("/"+test.Callback, withBodyHandler).Methods(test.Verb)
		} else {
			router.HandleFunc("/"+test.Callback, noBodyHandler).Methods(test.Verb)
		}
	}

	// called through dapr service invocation and meant to cause error
	router.HandleFunc("/timeouterror", timeoutServiceCall).Methods("POST")
	router.HandleFunc("/parseerror", parseErrorServiceCall).Methods("POST")
	router.HandleFunc("/largedatahttp", largeDataErrorServiceCallHTTP).Methods("POST")
	router.HandleFunc("/largedatagrpc", largeDataErrorServiceCallGrpc).Methods("POST")

	// called by test to run some cases - these will themselves make calls
	router.HandleFunc("/httptohttptest", httpTohttpTest).Methods("POST")
	router.HandleFunc("/grpctogrpctest", grpcToGrpcTest).Methods("POST")
	router.HandleFunc("/httptogrpctest", httpToGrpcTest).Methods("POST")
	router.HandleFunc("/grpctohttptest", grpcToHTTPTest).Methods("POST")
	router.HandleFunc("/badservicecalltesthttp", badServiceCallTestHTTP).Methods("POST")
	router.HandleFunc("/badservicecalltestgrpc", badServiceCallTestGrpc).Methods("POST")

	// called by Dapr invocation to ensure path separators are correctly
	// normalized, but not path segment contents.
	router.HandleFunc("/foo/%2E", echoPathHandler).Methods("GET", "POST")
	router.HandleFunc("/foo/%2Fbbb%2F%2E", echoPathHandler).Methods("GET", "POST")
	router.HandleFunc("/foo/%2Fb/bb%2F%2E", echoPathHandler).Methods("GET", "POST")

	// service invocation v1 e2e tests
	router.HandleFunc("/tests/dapr_id_httptohttptest", testDaprIDRequestHTTPToHTTP).Methods("POST")
	router.HandleFunc("/tests/v1_httptohttptest", testV1RequestHTTPToHTTP).Methods("POST")
	router.HandleFunc("/tests/v1_httptogrpctest", testV1RequestHTTPToGRPC).Methods("POST")
	router.HandleFunc("/tests/v1_grpctogrpctest", testV1RequestGRPCToGRPC).Methods("POST")
	router.HandleFunc("/tests/v1_grpctohttptest", testV1RequestGRPCToHTTP).Methods("POST")
	router.HandleFunc("/retrieve_request_object", retrieveRequestObject).Methods("POST")

	// Load balancing test
	router.HandleFunc("/tests/loadbalancing", testLoadBalancing).Methods("POST")

	// test path for Dapr method invocation decode
	router.PathPrefix("/path/").HandlerFunc(testPathHTTPCall)

	// service invocation to external HTTPEndpoint resources + through overwriting the URL
	router.HandleFunc("/httptohttptest_external", httpTohttpTestExternal).Methods("POST")
	router.HandleFunc("/tests/v1_httptohttptest_external", testV1RequestHTTPToHTTPExternal).Methods("POST") // headers

	router.Use(mux.CORSMethodMiddleware(router))

	return router
}

func retrieveRequestObject(w http.ResponseWriter, r *http.Request) {
	headers := map[string][]string{}
	for k, vals := range r.Header {
		headers[k] = vals
		log.Printf("headers: %s %q", k, vals)
	}

	serializedHeaders, _ := json.Marshal(headers)

	w.Header().Set("Content-Type", jsonContentType)
	w.Header().Set("DaprTest-Response-1", "DaprTest-Response-Value-1")
	w.Header().Set("DaprTest-Response-2", "DaprTest-Response-Value-2")
	w.Header().Add("DaprTest-Response-Multi", "DaprTest-Response-Multi-1")
	w.Header().Add("DaprTest-Response-Multi", "DaprTest-Response-Multi-2")

	if val, ok := headers["Daprtest-Traceid"]; ok {
		// val[0] is client app given trace id
		w.Header().Set("traceparent", val[0])
	}
	w.WriteHeader(http.StatusOK)
	w.Write(serializedHeaders)
}

func requestHTTPToHTTP(w http.ResponseWriter, r *http.Request, send func(remoteApp, method string, data []byte, headers http.Header) (*http.Response, error)) {
	var commandBody testCommandRequest
	err := json.NewDecoder(r.Body).Decode(&commandBody)
	if err != nil {
		onBadRequest(w, err)
		return
	}

	log.Printf("httpTohttpTest - target app: %s\n", commandBody.RemoteApp)

	daprAddress := fmt.Sprintf("localhost:%d", daprHTTPPort)

	log.Printf("dapr address is %s\n", daprAddress)
	testMessage := guuid.New().String()
	b, err := json.Marshal(testMessage)
	if err != nil {
		log.Printf("marshal had error %s\n", err)
		onSerializationFailed(w, err)
		return
	}

	log.Printf("httpTohttpTest calling with message %s\n", string(b))
	headers := map[string][]string{
		"DaprTest-Request-1": {"DaprValue1"},
		"DaprTest-Request-2": {"DaprValue2"},
		"DaprTest-Multi":     {"M'illumino", "d'immenso"},
	}

	tracing, _ := strconv.ParseBool(commandBody.RemoteAppTracing)
	if tracing {
		headers["Daprtest-Traceid"] = []string{"00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"}
	}

	resp, err := send(commandBody.RemoteApp, "retrieve_request_object", b, headers)
	if err != nil {
		log.Printf("response had error %s\n", err)
		onHTTPCallFailed(w, 0, err)
		return
	}

	respHeaders := map[string][]string{}
	for k, vals := range resp.Header {
		respHeaders[k] = vals
	}
	respHeaderString, _ := json.Marshal(respHeaders)

	reqHeadersString, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		onBadRequest(w, err)
		return
	}

	respMessage := map[string]string{
		"request":  string(reqHeadersString),
		"response": string(respHeaderString),
	}

	respBody, err := json.Marshal(respMessage)
	if err != nil {
		onBadRequest(w, err)
		return
	}

	log.Printf("response was %s", respBody)

	logAndSetResponse(w, http.StatusOK, string(respBody))
}

func requestHTTPToHTTPExternal(w http.ResponseWriter, r *http.Request, send func(remoteApp, method string, data []byte, headers http.Header) (*http.Response, error)) {
	var commandBody testCommandRequestExternal
	err := json.NewDecoder(r.Body).Decode(&commandBody)
	if err != nil {
		onBadRequest(w, err)
		return
	}

	log.Printf("httpTohttpTestExternal - target app: %s\n", commandBody.RemoteApp)

	daprAddress := fmt.Sprintf("localhost:%d", daprHTTPPort)

	log.Printf("dapr address is %s\n", daprAddress)
	testMessage := guuid.New().String()
	b, err := json.Marshal(testMessage)
	if err != nil {
		log.Printf("marshal had error %s\n", err)
		onSerializationFailed(w, err)
		return
	}

	log.Printf("httpTohttpTest calling with message %s\n", string(b))
	headers := map[string][]string{
		"DaprTest-Request-1": {"DaprValue1"},
		"DaprTest-Request-2": {"DaprValue2"},
		"DaprTest-Multi":     {"M'illumino", "d'immenso"},
	}

	tracing, _ := strconv.ParseBool(commandBody.RemoteAppTracing)
	if tracing {
		headers["Daprtest-Traceid"] = []string{"00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"}
	}

	// case of overwritten URL
	var remoteApp string
	if commandBody.ExternalIP == "" {
		remoteApp = commandBody.RemoteApp
	} else {
		remoteApp = sanitizeHTTPURL(commandBody.ExternalIP)
	}

	resp, err := send(remoteApp, "retrieve_request_object", b, headers)
	if err != nil {
		log.Printf("response had error %s\n", err)
		onHTTPCallFailed(w, 0, err)
		return
	}

	respHeaders := map[string][]string{}
	for k, vals := range resp.Header {
		respHeaders[k] = vals
	}
	respHeaderString, _ := json.Marshal(respHeaders)

	reqHeadersString, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		onBadRequest(w, err)
		return
	}

	respMessage := map[string]string{
		"request":  string(reqHeadersString),
		"response": string(respHeaderString),
	}

	respBody, err := json.Marshal(respMessage)
	if err != nil {
		onBadRequest(w, err)
		return
	}

	log.Printf("response was %s\n", respBody)

	logAndSetResponse(w, http.StatusOK, string(respBody))
}

func testLoadBalancing(w http.ResponseWriter, r *http.Request) {
	log.Println("Enter load balancing test")
	res, err := invokeServiceWithBodyHeader("serviceinvocation-callee-2", "pid", nil, nil)
	if err != nil {
		onBadRequest(w, err)
		return
	}
	defer res.Body.Close()

	w.WriteHeader(res.StatusCode)

	log.Print("Response: ")
	io.Copy(io.MultiWriter(w, os.Stdout), res.Body)
}

// testDaprIDRequestHTTPToHTTP calls from http caller to http callee without requiring the caller to use Dapr style URL.
func testDaprIDRequestHTTPToHTTP(w http.ResponseWriter, r *http.Request) {
	log.Println("Enter service invocation with dapr-app-id header and shorter URL - http -> http")
	requestHTTPToHTTP(w, r, invokeServiceWithDaprAppIDHeader)
}

// testV1RequestHTTPToHTTP calls from http caller to http callee
func testV1RequestHTTPToHTTP(w http.ResponseWriter, r *http.Request) {
	log.Println("Enter service invocation v1 - http -> http")
	requestHTTPToHTTP(w, r, invokeServiceWithBodyHeader)
}

// testV1RequestHTTPToHTTPExternal calls from http caller to http callee
func testV1RequestHTTPToHTTPExternal(w http.ResponseWriter, r *http.Request) {
	log.Println("Enter service invocation external v1 - http -> http")
	requestHTTPToHTTPExternal(w, r, invokeServiceWithBodyHeader)
}

// testV1RequestHTTPToGRPC calls from http caller to grpc callee
func testV1RequestHTTPToGRPC(w http.ResponseWriter, r *http.Request) {
	log.Println("Enter service invocation v1 - http -> grpc")
	var commandBody testCommandRequest
	err := json.NewDecoder(r.Body).Decode(&commandBody)
	if err != nil {
		onBadRequest(w, err)
		return
	}

	log.Printf("httpTogrpcTest - target app: %s\n", commandBody.RemoteApp)

	daprAddress := fmt.Sprintf("localhost:%d", daprHTTPPort)

	log.Printf("dapr address is %s\n", daprAddress)
	testMessage := guuid.New().String()
	b, err := json.Marshal(testMessage)
	if err != nil {
		log.Printf("marshal had error %s\n", err)
		onSerializationFailed(w, err)
		return
	}

	log.Printf("httpTogrpcTest calling with message %s\n", string(b))
	headers := map[string][]string{
		"DaprTest-Request-1": {"DaprValue1"},
		"DaprTest-Request-2": {"DaprValue2"},
		"DaprTest-Multi":     {"M'illumino", "d'immenso"},
	}

	tracing, _ := strconv.ParseBool(commandBody.RemoteAppTracing)
	if tracing {
		headers["Daprtest-Traceid"] = []string{"00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"}
	}
	resp, err := invokeServiceWithBodyHeader(commandBody.RemoteApp, "retrieve_request_object", b, headers)
	if err != nil {
		log.Printf("response had error %s\n", err)
		onHTTPCallFailed(w, 0, err)
		return
	}

	respHeaders := map[string][]string{}
	for k, vals := range resp.Header {
		respHeaders[k] = vals
	}
	respHeaderString, _ := json.Marshal(respHeaders)

	reqHeadersString, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		onBadRequest(w, err)
		return
	}

	respMessage := map[string]string{
		"request":  string(reqHeadersString),
		"response": string(respHeaderString),
	}

	respBody, err := json.Marshal(respMessage)
	if err != nil {
		onBadRequest(w, err)
		return
	}

	log.Printf("response was %s\n", respBody)

	logAndSetResponse(w, http.StatusOK, string(respBody))
}

// testV1RequestGRPCToGRPC calls from http caller to grpc callee
func testV1RequestGRPCToGRPC(w http.ResponseWriter, r *http.Request) {
	log.Println("Enter service invocation v1 - grpc -> grpc")
	var commandBody testCommandRequest
	err := json.NewDecoder(r.Body).Decode(&commandBody)
	if err != nil {
		onBadRequest(w, err)
		return
	}

	log.Printf("gRPCTogRPCTest - target app: %s\n", commandBody.RemoteApp)

	// Create the client
	tracing, _ := strconv.ParseBool(commandBody.RemoteAppTracing)
	ctx := r.Context()
	md, ok := metadata.FromOutgoingContext(ctx)
	if !ok {
		md = metadata.MD{}
	}
	md.Append("DaprTest-Request-1", "DaprValue1")
	md.Append("DaprTest-Request-2", "DaprValue2")
	md.Append("DaprTest-Multi", "M'illumino", "d'immenso")
	if tracing {
		md.Append("Daprtest-Traceid", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	}
	ctx = metadata.NewOutgoingContext(ctx, md)
	req := &runtimev1pb.InvokeServiceRequest{
		Id: commandBody.RemoteApp,
		Message: &commonv1pb.InvokeRequest{
			Method:      "retrieve_request_object",
			Data:        &anypb.Any{Value: []byte("GRPCToGRPCTest")},
			ContentType: "text/plain; utf-8",
		},
	}

	var header, trailer metadata.MD
	resp, err := daprClient.InvokeService(
		ctx,
		req,
		grpc.Header(&header),   // will retrieve header
		grpc.Trailer(&trailer), // will retrieve trailer
	)
	if err != nil {
		log.Printf("response had error %s\n", err)
		onHTTPCallFailed(w, 0, err)
		return
	}

	reqHeadersString := resp.GetData().GetValue()

	respHeaders := map[string][]string{}
	for k, vals := range header {
		var listValue []string
		if strings.HasSuffix(k, "-bin") {
			for _, val := range vals {
				listValue = append(listValue, base64.StdEncoding.EncodeToString([]byte(val)))
			}
		} else {
			listValue = vals
		}
		respHeaders[k] = listValue
	}

	respHeaderString, _ := json.Marshal(respHeaders)

	respTrailers := map[string][]string{}

	for k, vals := range trailer {
		respTrailers[k] = vals
	}
	respTrailerString, _ := json.Marshal(respTrailers)

	respMessage := map[string]string{
		"request":  string(reqHeadersString),
		"response": string(respHeaderString),
		"trailers": string(respTrailerString),
	}

	respBody, err := json.Marshal(respMessage)
	if err != nil {
		onBadRequest(w, err)
		return
	}

	log.Printf("response was %s\n", respBody)

	logAndSetResponse(w, http.StatusOK, string(respBody))
}

// testV1RequestGRPCToHTTP calls from grpc caller to http callee
func testV1RequestGRPCToHTTP(w http.ResponseWriter, r *http.Request) {
	log.Println("Enter service invocation v1 - grpc -> http")
	var commandBody testCommandRequest
	err := json.NewDecoder(r.Body).Decode(&commandBody)
	if err != nil {
		onBadRequest(w, err)
		return
	}

	log.Printf("grpcToHTTPTest - target app: %s\n", commandBody.RemoteApp)

	tracing, _ := strconv.ParseBool(commandBody.RemoteAppTracing)
	ctx := r.Context()
	md, ok := metadata.FromOutgoingContext(ctx)
	if !ok {
		md = metadata.MD{}
	}
	md.Append("DaprTest-Request-1", "DaprValue1")
	md.Append("DaprTest-Request-2", "DaprValue2")
	md.Append("DaprTest-Multi", "M'illumino", "d'immenso")
	if tracing {
		md.Append("Daprtest-Traceid", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	}
	ctx = metadata.NewOutgoingContext(ctx, md)

	req := &runtimev1pb.InvokeServiceRequest{
		Id: commandBody.RemoteApp,
		Message: &commonv1pb.InvokeRequest{
			Method:      "retrieve_request_object",
			Data:        &anypb.Any{Value: []byte("GRPCToHTTPTest")},
			ContentType: "text/plain; utf-8",
			HttpExtension: &commonv1pb.HTTPExtension{
				Verb: commonv1pb.HTTPExtension_POST, //nolint:nosnakecase
			},
		},
	}
	var header metadata.MD
	resp, err := daprClient.InvokeService(
		ctx,
		req,
		grpc.Header(&header), // will retrieve header
	)
	if err != nil {
		log.Printf("response had error %s\n", err)
		onHTTPCallFailed(w, 0, err)
		return
	}

	reqHeadersString := resp.GetData().GetValue()

	respHeaders := map[string][]string{}
	for k, vals := range header {
		var listValue []string
		if strings.HasSuffix(k, "-bin") {
			for _, val := range vals {
				listValue = append(listValue, base64.StdEncoding.EncodeToString([]byte(val)))
			}
		} else {
			listValue = vals
		}
		respHeaders[k] = listValue
	}

	respHeaderString, _ := json.Marshal(respHeaders)

	respMessage := map[string]string{
		"request":  string(reqHeadersString),
		"response": string(respHeaderString),
	}

	respBody, err := json.Marshal(respMessage)
	if err != nil {
		onBadRequest(w, err)
		return
	}

	log.Printf("response was %s\n", respBody)

	logAndSetResponse(w, http.StatusOK, string(respBody))
}

// Performs calls from grpc client to grpc server.  It sends a random string to the other app
// and expects the response to contain the same string inside an appResponse.
func grpcToGrpcTest(w http.ResponseWriter, r *http.Request) {
	log.Println("Enter grpcToGrpcTest")
	var commandBody testCommandRequest
	err := json.NewDecoder(r.Body).Decode(&commandBody)
	if err != nil {
		onBadRequest(w, err)
		return
	}

	log.Printf("%s - target app: %s\n", commandBody.Method, commandBody.RemoteApp)

	testMessage := guuid.New().String()
	b, err := json.Marshal(testMessage)
	if err != nil {
		log.Printf("marshal had error %s\n", err)
		onSerializationFailed(w, err)
		return
	}

	log.Printf("%s calling with message %s\n", commandBody.Method, string(b))

	req := constructRequest(commandBody.RemoteApp, commandBody.Method, "", b)
	resp, err := daprClient.InvokeService(r.Context(), req)
	if err != nil {
		logAndSetResponse(w, http.StatusInternalServerError, "grpc call failed with "+err.Error())
		return
	}

	body := resp.GetData().GetValue()
	log.Printf("resp was %s\n", string(body))

	var responseMessage appResponse
	err = json.Unmarshal(body, &responseMessage)
	if err != nil {
		onDeserializationFailed(w, err)
		return
	}

	// validate response ends with "[testMessage] | [httpMethod]"
	if testMessage != responseMessage.Message {
		errorMessage := "Expected " + testMessage + " received " + responseMessage.Message
		logAndSetResponse(w, http.StatusInternalServerError, errorMessage)
		return
	}

	// caller of this method doesn't inspect response, it only looks for status
	logAndSetResponse(w, http.StatusOK, "success")
}

// Performs calls from http client to grpc server.  It sends a random string to the other app
// and expects the response to contain the same string inside an appResponse.
func httpToGrpcTest(w http.ResponseWriter, r *http.Request) {
	log.Println("Enter httpToGrpcTest")
	var commandBody testCommandRequest
	err := json.NewDecoder(r.Body).Decode(&commandBody)
	if err != nil {
		onBadRequest(w, err)
		return
	}

	log.Printf("httpToGrpcTest - target app: %s\n", commandBody.RemoteApp)

	daprAddress := fmt.Sprintf("localhost:%d", daprHTTPPort)

	log.Printf("dapr address is %s\n", daprAddress)
	testMessage := guuid.New().String()
	b, err := json.Marshal(testMessage)
	if err != nil {
		log.Printf("marshal had error %s\n", err)
		onSerializationFailed(w, err)
		return
	}

	log.Printf("httpToGrpcTest calling with message %s\n", string(b))
	resp, statusCode, err := invokeServiceWithBody(commandBody.RemoteApp, "httpToGrpcTest", b)
	if err != nil {
		log.Printf("response had error %s\n", err)
		onHTTPCallFailed(w, statusCode, err)
		return
	}

	log.Printf("response was %s\n", resp.Message)

	logAndSetResponse(w, http.StatusOK, "success")
}

// Performs calls from http client to http server.  It sends a random string to the other app
// and expects the response to contain the same string inside an appResponse.
// It calls methods with all 4 http methods (verbs)
func httpTohttpTest(w http.ResponseWriter, r *http.Request) {
	log.Println("Enter httpTohttpTest")
	defer r.Body.Close()
	var commandBody testCommandRequest
	err := json.NewDecoder(r.Body).Decode(&commandBody)
	if err != nil {
		onBadRequest(w, err)
		return
	}

	log.Printf("httpTohttpTest - target app: %s\n", commandBody.RemoteApp)

	daprAddress := fmt.Sprintf("localhost:%d", daprHTTPPort)

	log.Printf("dapr address is %s\n", daprAddress)
	testMessage := guuid.New().String()

	log.Printf("httpTohttpTest calling with message %s\n", testMessage)

	for _, test := range testMethods {
		testMessage := "ok"
		if test.SendBody {
			if commandBody.Message != nil {
				testMessage = *commandBody.Message
			} else {
				testMessage = guuid.New().String()
			}
		}
		url := fmt.Sprintf(
			"http://localhost:%s/v1.0/invoke/%s/method/%s",
			strconv.Itoa(daprHTTPPort), commandBody.RemoteApp,
			test.Callback)
		log.Printf("%s invoke url is %s\n", test.Verb, url)
		var b []byte

		if test.SendBody {
			var err error
			b, err = json.Marshal(testMessage)
			if err != nil {
				log.Printf("marshal had error %s\n", err)
				onSerializationFailed(w, err)
				return
			}
			log.Printf("sending body: %s\n", string(b))
		}

		resp, err := httpWrapper(test.Verb, url, b)
		if err != nil {
			log.Printf("response had error %s\n", err)
			onHTTPCallFailed(w, 0, err)
			return
		}

		if test.ExpectBody && testMessage != resp.Message {
			errorMessage := "Expected " + testMessage + " received " + resp.Message
			logAndSetResponse(w, http.StatusInternalServerError, errorMessage)
			return
		}

		log.Printf("httpTohttpTest - %s test successful\n", test.Verb)
	}

	logAndSetResponse(w, http.StatusOK, "success")
}

// Performs calls from http client to http server.
// It sends a random string to the non-Daprized app,
// and expects the response to contain the same string inside an appResponse.
func httpTohttpTestExternal(w http.ResponseWriter, r *http.Request) {
	log.Println("Enter httpTohttpTestExternal")
	defer r.Body.Close()
	var commandBody testCommandRequestExternal
	err := json.NewDecoder(r.Body).Decode(&commandBody)
	if err != nil {
		onBadRequest(w, err)
		return
	}
	daprAddress := fmt.Sprintf("localhost:%d", daprHTTPPort)

	log.Printf("dapr address is %s", daprAddress)
	log.Printf("httpTohttpTestExternal calling with method %s", commandBody.Method)

	for _, test := range testMethods {
		testMessage := "success"
		if test.SendBody {
			if commandBody.Message != nil {
				testMessage = *commandBody.Message
			} else {
				testMessage = guuid.New().String()
			}
		}
		// case of overwritten URL
		var url string
		if commandBody.ExternalIP == "" {
			url = fmt.Sprintf(
				"http://localhost:%s/v1.0/invoke/%s/method/%s",
				strconv.Itoa(daprHTTPPort), commandBody.RemoteApp,
				commandBody.Method)
		} else {
			url = fmt.Sprintf(
				"http://localhost:%s/v1.0/invoke/%s/method/%s",
				strconv.Itoa(daprHTTPPort), sanitizeHTTPURL(commandBody.ExternalIP),
				commandBody.Method)
		}

		log.Printf("%s invoke url is %s", test.Verb, url)
		var b []byte

		if test.SendBody {
			var err error
			b, err = json.Marshal(testMessage)
			if err != nil {
				log.Printf("marshal had error %s", err)
				onSerializationFailed(w, err)
				return
			}
			log.Printf("sending body: %s", string(b))
		}

		resp, err := httpWrapper(test.Verb, url, b)
		if err != nil {
			log.Printf("response had error %s", err)
			onHTTPCallFailed(w, 0, err)
			return
		}

		if test.ExpectBody && testMessage != resp.Message {
			errorMessage := "Expected " + testMessage + " received " + resp.Message
			logAndSetResponse(w, http.StatusInternalServerError, errorMessage)
			return
		}

		log.Printf("httpTohttpTestExternal - %s test successful", test.Verb)
	}

	logAndSetResponse(w, http.StatusOK, "success")
}

// data should be serialized by caller
func httpWrapper(httpMethod string, url string, data []byte) (appResponse, error) {
	var appResp appResponse

	var requestBody io.Reader
	if len(data) > 0 {
		requestBody = bytes.NewReader(data)
	}

	req, err := http.NewRequest(httpMethod, sanitizeHTTPURL(url), requestBody)
	if err != nil {
		return appResp, err
	}
	res, err := httpClient.Do(req)
	if res != nil {
		log.Printf("res in httpWrapper() %v with status code %d", res, res.StatusCode)
	}
	log.Printf("res error in httpWrapper() %v", err)
	if err != nil {
		return appResp, err
	}

	defer func() {
		// Drain before closing
		_, _ = io.Copy(io.Discard, res.Body)
		_ = res.Body.Close()
	}()

	actualVerb := res.Header.Get("x-dapr-tests-request-method")
	if httpMethod != actualVerb {
		return appResp, fmt.Errorf("Expected HTTP verb: %s actual %s", httpMethod, actualVerb) //nolint:stylecheck
	}

	body, err := io.ReadAll(res.Body)
	log.Printf("response body: %s\n", string(body))
	if err != nil {
		return appResp, err
	}
	if len(body) > 0 {
		err = json.Unmarshal(body, &appResp)
		if err != nil {
			return appResp, err
		}
	}

	return appResp, nil
}

// Performs calls from grpc client to http server.  It sends a random string to the other app
// and expects the response to contain the same string inside an appResponse.
// It uses all 4 http methods (verbs) in metadata to invoke the proper http method.
func grpcToHTTPTest(w http.ResponseWriter, r *http.Request) {
	fmt.Println("Enter grpcToHTTPTest")
	var commandBody testCommandRequest
	err := json.NewDecoder(r.Body).Decode(&commandBody)
	if err != nil {
		onBadRequest(w, err)
		return
	}

	fmt.Printf("grpcToHTTPTest - target app: %s\n", commandBody.RemoteApp)

	var b []byte
	for _, v := range httpMethods {
		testMessage := guuid.New().String()
		b, err = json.Marshal(testMessage)
		if err != nil {
			fmt.Printf("marshal had error %s\n", err)
			onSerializationFailed(w, err)
			return
		}

		fmt.Printf("grpcToHTTPTest calling with verb %s, message %s\n", v, testMessage)
		req := constructRequest(commandBody.RemoteApp, "posthandler", v, b)

		var resp *commonv1pb.InvokeResponse
		var err error
		switch v {
		case "POST":
			req.Message.Method = "posthandler"
		case "GET":
			req.Message.Method = "gethandler"
		case "PUT":
			req.Message.Method = "puthandler"
		case "DELETE":
			req.Message.Method = "deletehandler"
		default:
			fmt.Println("Unexpected option")
			if err != nil {
				logAndSetResponse(w, http.StatusInternalServerError, "unexpected option "+v)
				return
			}
		}

		resp, err = daprClient.InvokeService(r.Context(), req)
		if err != nil {
			logAndSetResponse(w, http.StatusInternalServerError, "error returned from grpc client")
			return
		}

		body := resp.GetData().GetValue()

		fmt.Printf("resp was %s\n", string(body))
		// var responseMessage string
		var appResp appResponse
		err = json.Unmarshal(body, &appResp)
		if err != nil {
			onDeserializationFailed(w, err)
			return
		}

		// We don't send a body with GET.  The receiving method just
		// returns the body, so skip.
		if v == "GET" {
			continue
		}

		if testMessage != appResp.Message {
			errorMessage := "Expected " + testMessage + " received " + appResp.Message
			logAndSetResponse(w, http.StatusInternalServerError, errorMessage)
			return
		}
	}

	logAndSetResponse(w, http.StatusOK, "success")
}

func badServiceCallTestHTTP(w http.ResponseWriter, r *http.Request) {
	fmt.Println("Enter badServiceCallTestHTTP")
	var commandBody testCommandRequest
	err := json.NewDecoder(r.Body).Decode(&commandBody)
	if err != nil {
		onBadRequest(w, err)
		return
	}

	fmt.Printf("badServiceCallTestHTTP - target app: %s\n", commandBody.RemoteApp)

	daprAddress := fmt.Sprintf("localhost:%s", strconv.Itoa(daprHTTPPort))

	fmt.Printf("dapr address is %s\n", daprAddress)
	var testMessage struct {
		Data string `json:"data"`
	}
	testMessage.Data = guuid.New().String()

	fmt.Printf("badServiceCallTestHTTP calling with message %v\n", testMessage)

	// post
	url := fmt.Sprintf(
		"http://localhost:%s/v1.0/invoke/%s/method/%s",
		strconv.Itoa(daprHTTPPort),
		commandBody.RemoteApp,
		commandBody.Method,
	)
	fmt.Printf("%s invoke url is %s\n", commandBody.Method, url)
	b, err := json.Marshal(testMessage)
	if err != nil {
		fmt.Printf("marshal had error %s\n", err)
		onSerializationFailed(w, err)
		return
	}

	fmt.Printf("%s method body is %s\n", commandBody.Method, string(b))

	var testResponse negativeTestResult

	prevTimeout := httpClient.Timeout
	// Explicitly set the timeout to force an error
	if commandBody.Method == "timeouterror" {
		httpClient.Timeout = 5 * time.Second
	}
	resp, err := httpClient.Post(sanitizeHTTPURL(url), jsonContentType, bytes.NewReader(b))
	if commandBody.Method == "timeouterror" {
		httpClient.Timeout = prevTimeout
	}

	testResponse.MainCallSuccessful = err == nil && resp.StatusCode == 200

	if resp != nil && resp.Body != nil {
		fmt.Printf("badServiceCallTestHTTP - Response Code: %d\n", resp.StatusCode)
		w.WriteHeader(resp.StatusCode)
		rawBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		testResponse.RawBody = rawBody
		json.NewDecoder(strings.NewReader(string(rawBody))).Decode(&testResponse.Results)
	}

	if err != nil {
		testResponse.RawError = err.Error()
		if resp == nil {
			w.WriteHeader(http.StatusInternalServerError)
		}
	}

	json.NewEncoder(w).Encode(testResponse)
}

// echoPathHandler is a test endpoint that returns the path of the request as
// is.
func echoPathHandler(w http.ResponseWriter, r *http.Request) {
	w.Write([]byte(r.URL.EscapedPath()))
	w.WriteHeader(http.StatusOK)
}

func badServiceCallTestGrpc(w http.ResponseWriter, r *http.Request) {
	fmt.Println("Enter badServiceCallTestGrpc")
	var commandBody testCommandRequest
	err := json.NewDecoder(r.Body).Decode(&commandBody)
	if err != nil {
		onBadRequest(w, err)
		return
	}

	fmt.Printf("badServiceCallTestGrpc - target app: %s\n", commandBody.RemoteApp)

	timeoutDuration := 30 * time.Second
	// Shorten the timeout if we want to force the error
	if commandBody.Method == "timeouterror" {
		timeoutDuration = 5 * time.Second
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeoutDuration)
	defer cancel()

	var testMessage struct {
		Data string `json:"data"`
	}
	testMessage.Data = guuid.New().String()

	fmt.Printf("badServiceCallTestGrpc calling with message %v\n", testMessage)

	b, _ := json.Marshal(testMessage)
	req := constructRequest(commandBody.RemoteApp, commandBody.Method, "POST", b)

	var testResponse negativeTestResult
	resp, err := daprClient.InvokeService(ctx, req)
	testResponse.MainCallSuccessful = true
	if err != nil {
		testResponse.MainCallSuccessful = false
		testResponse.RawError = err.Error()
		w.WriteHeader(http.StatusInternalServerError)
	}

	if resp != nil && resp.GetData() != nil {
		rawBody := resp.GetData().GetValue()
		testResponse.RawBody = rawBody
		json.NewDecoder(strings.NewReader(string(rawBody))).Decode(&testResponse.Results)
	}

	json.NewEncoder(w).Encode(testResponse)
}

func timeoutServiceCall(w http.ResponseWriter, r *http.Request) {
	fmt.Println("Entering timeoutServiceCall")

	// Timeout for the client is 5 seconds, we just need to wait longer than that.
	time.Sleep(10 * time.Second)

	logAndSetResponse(w, 200, "Client waited longer than it should have.")
}

func parseErrorServiceCall(w http.ResponseWriter, r *http.Request) {
	fmt.Println("Entering parseErrorServiceCall")
	var data struct {
		Data int `json:"data"`
	}

	err := json.NewDecoder(r.Body).Decode(&data)
	if err != nil {
		onSerializationFailed(w, err)
		return
	}

	// We shouldn't get here.
	logAndSetResponse(w, 200, fmt.Sprintf("Request should have failed to parse: %v", data))
}

func largeDataErrorServiceCallHTTP(w http.ResponseWriter, r *http.Request) {
	largeDataErrorServiceCall(w, r, true)
}

func largeDataErrorServiceCallGrpc(w http.ResponseWriter, r *http.Request) {
	largeDataErrorServiceCall(w, r, false)
}

func largeDataErrorServiceCall(w http.ResponseWriter, r *http.Request, isHTTP bool) {
	fmt.Println("Entering largeDataErrorServiceCall")

	// post
	url := fmt.Sprintf(
		"http://localhost:%s/v1.0/invoke/serviceinvocation-callee-0/method/posthandler",
		strconv.Itoa(daprHTTPPort))

	testSizes := []struct {
		size int
		name string
	}{
		{
			size: 1024 * 1024,
			name: "1MB",
		},
		{
			size: 1024*1024*3 - 454,
			name: "4MB",
		},
		{
			size: 1024*1024*3 + 10,
			name: "4MB+",
		},
		{
			size: 1024 * 1024 * 8,
			name: "8MB",
		},
	}

	results := make([]individualTestResult, len(testSizes))
	for i, test := range testSizes {
		var result individualTestResult
		result.TestCase = test.name

		body := make([]byte, test.size)
		jsonBody, _ := json.Marshal(body)
		fmt.Printf("largeDataErrorServiceCall %s - Request size: %d\n", test.name, len(jsonBody))

		if isHTTP {
			resp, err := httpClient.Post(sanitizeHTTPURL(url), jsonContentType, bytes.NewReader(jsonBody))
			result.CallSuccessful = !((resp != nil && resp.StatusCode != 200) || err != nil)
			if resp != nil {
				// Drain before closing
				_, _ = io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
			}
		} else {
			req := constructRequest("serviceinvocation-callee-0", "posthandler", "POST", jsonBody)
			_, err := daprClient.InvokeService(r.Context(), req)
			result.CallSuccessful = err == nil
		}

		results[i] = result
	}

	json.NewEncoder(w).Encode(results)
}

// testPathHTTPCall return the path received form request.
func testPathHTTPCall(w http.ResponseWriter, r *http.Request) {
	logAndSetResponse(w, http.StatusOK, r.RequestURI)
}

func main() {
	daprClient = utils.GetGRPCClient(daprGRPCPort)

	httpMethods = []string{"POST", "GET", "PUT", "DELETE"}

	// Generate a random "process ID" of 48 bits
	pidBytes := make([]byte, 6)
	io.ReadFull(rand.Reader, pidBytes)
	pid = hex.EncodeToString(pidBytes)

	log.Printf("Dapr service_invocation - listening on http://localhost:%d", appPort)
	log.Printf("PID: %s", pid)
	utils.StartServer(appPort, appRouter, true, false)
}

// Bad http request
func onBadRequest(w http.ResponseWriter, err error) {
	msg := "deserialization failed with " + err.Error()
	logAndSetResponse(w, http.StatusBadRequest, msg)
}

func onDeserializationFailed(w http.ResponseWriter, err error) {
	msg := "deserialization failed with " + err.Error()
	logAndSetResponse(w, http.StatusInternalServerError, msg)
}

func onSerializationFailed(w http.ResponseWriter, err error) {
	msg := "serialization failed with " + err.Error()
	logAndSetResponse(w, http.StatusInternalServerError, msg)
}

func onHTTPCallFailed(w http.ResponseWriter, statusCode int, err error) {
	msg := "HTTP call failed with " + err.Error()
	if statusCode == 0 {
		statusCode = http.StatusInternalServerError
	}
	logAndSetResponse(w, statusCode, msg)
}

func logAndSetResponse(w http.ResponseWriter, statusCode int, message string) {
	log.Println(message)

	w.WriteHeader(statusCode)
	json.NewEncoder(w).
		Encode(appResponse{Message: message})
}

//--- The functions below are copied from utils/helpers.go to workaround the package include in a container problem.

func sanitizeHTTPURL(url string) string {
	if !strings.Contains(url, "http") {
		url = fmt.Sprintf("http://%s", url)
	}

	return url
}
