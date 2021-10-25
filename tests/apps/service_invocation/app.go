// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	guuid "github.com/google/uuid"
	"github.com/gorilla/mux"

	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/anypb"
)

var httpMethods []string

const (
	appPort  = 3000
	daprPort = 3500

	jsonContentType = "application/json"
)

type testCommandRequest struct {
	RemoteApp        string `json:"remoteApp,omitempty"`
	Method           string `json:"method,omitempty"`
	RemoteAppTracing string `json:"remoteAppTracing"`
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

var testMethods []httpTestMethods = []httpTestMethods{
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
	{
		Verb:       "CONNECT",
		Callback:   "connecthandler",
		SendBody:   true,
		ExpectBody: true,
	},
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

var httpClient = newHTTPClient()

var (
	grpcConn   *grpc.ClientConn
	daprClient runtimev1pb.DaprClient
)

// indexHandler is the handler for root path
func indexHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("indexHandler is called\n")
	fmt.Print("indexHalder is called 2\n")

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(appResponse{Message: "OK"})
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
	fmt.Printf("withBodyHandler called. HTTP Verb: %s\n", r.Method)
	var s string
	err := json.NewDecoder(r.Body).Decode(&s)
	if err != nil {
		onBadRequest(w, err)
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

func testHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Println("Enter testHandler")
	var commandBody testCommandRequest
	err := json.NewDecoder(r.Body).Decode(&commandBody)
	if err != nil {
		onBadRequest(w, err)
		return
	}

	fmt.Printf("  testHandler invoking %s with method %s\n", commandBody.RemoteApp, commandBody.Method)
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
	resp, err := invokeServiceWithBodyHeader(remoteApp, method, data, map[string]string{})

	if err != nil {
		return appResponse{}, resp.StatusCode, err
	}

	body, err := io.ReadAll(resp.Body)
	defer resp.Body.Close()
	if err != nil {
		return appResponse{}, resp.StatusCode, err
	}

	var appResp appResponse
	err = json.Unmarshal(body, &appResp)
	if err != nil {
		return appResponse{}, resp.StatusCode, err
	}

	// invokeServiceWithBodyHeader uses http client.Do method which
	// returns success for everything except 2xx error codes. Check
	// the status code to extract non 2xx errors.
	if resp.StatusCode != http.StatusOK {
		return appResponse{}, resp.StatusCode, errors.New(appResp.Message)
	}

	return appResp, resp.StatusCode, nil
}

func invokeServiceWithBodyHeader(remoteApp, method string, data []byte, headers map[string]string) (*http.Response, error) {
	url := fmt.Sprintf("http://localhost:%s/v1.0/invoke/%s/method/%s", strconv.Itoa(daprPort), remoteApp, method)
	fmt.Printf("invoke url is %s\n", url)

	var t io.Reader = nil
	if data != nil {
		t = bytes.NewBuffer(data)
	}

	/* #nosec */
	req, _ := http.NewRequest("POST", url, t)
	for k, v := range headers {
		req.Header.Add(k, v)
	}

	return httpClient.Do(req)
}

func invokeServiceWithDaprAppIDHeader(remoteApp, method string, data []byte, headers map[string]string) (*http.Response, error) {
	url := fmt.Sprintf("http://localhost:%s/%s", strconv.Itoa(daprPort), method)
	fmt.Printf("invoke url is %s\n", url)

	var t io.Reader = nil
	if data != nil {
		t = bytes.NewBuffer(data)
	}

	/* #nosec */
	req, _ := http.NewRequest("POST", url, t)
	req.Header.Add("dapr-app-id", remoteApp)
	for k, v := range headers {
		req.Header.Add(k, v)
	}

	return httpClient.Do(req)
}

func constructRequest(id, method, httpVerb string, body []byte) *runtimev1pb.InvokeServiceRequest {
	msg := &commonv1pb.InvokeRequest{Method: method}
	msg.ContentType = jsonContentType
	msg.Data = &anypb.Any{Value: body}
	if httpVerb != "" {
		msg.HttpExtension = &commonv1pb.HTTPExtension{
			Verb: commonv1pb.HTTPExtension_Verb(commonv1pb.HTTPExtension_Verb_value[httpVerb]),
		}
	}

	return &runtimev1pb.InvokeServiceRequest{
		Id:      id,
		Message: msg,
	}
}

// appRouter initializes restful api router
func appRouter() *mux.Router {
	router := mux.NewRouter().StrictSlash(true)

	router.HandleFunc("/", indexHandler).Methods("GET")
	router.HandleFunc("/singlehop", singlehopHandler).Methods("POST")
	router.HandleFunc("/multihop", multihopHandler).Methods("POST")

	router.HandleFunc("/opAllow", opAllowHandler).Methods("POST")
	router.HandleFunc("/opDeny", opDenyHandler).Methods("POST")

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

	// service invocation v1 e2e tests
	router.HandleFunc("/tests/dapr_id_httptohttptest", testDaprIDRequestHTTPToHTTP).Methods("POST")
	router.HandleFunc("/tests/v1_httptohttptest", testV1RequestHTTPToHTTP).Methods("POST")
	router.HandleFunc("/tests/v1_httptogrpctest", testV1RequestHTTPToGRPC).Methods("POST")
	router.HandleFunc("/tests/v1_grpctogrpctest", testV1RequestGRPCToGRPC).Methods("POST")
	router.HandleFunc("/tests/v1_grpctohttptest", testV1RequestGRPCToHTTP).Methods("POST")
	router.HandleFunc("/retrieve_request_object", retrieveRequestObject).Methods("POST")

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

	w.Header().Set("Content-Type", "application/json; utf-8")
	w.Header().Set("DaprTest-Response-1", "DaprTest-Response-Value-1")
	w.Header().Set("DaprTest-Response-2", "DaprTest-Response-Value-2")

	if val, ok := headers["Daprtest-Traceid"]; ok {
		// val[0] is client app given trace id
		w.Header().Set("traceparent", val[0])
	}
	w.WriteHeader(http.StatusOK)
	w.Write(serializedHeaders)
}

func requestHTTPToHTTP(w http.ResponseWriter, r *http.Request, send func(remoteApp, method string, data []byte, headers map[string]string) (*http.Response, error)) {
	var commandBody testCommandRequest
	err := json.NewDecoder(r.Body).Decode(&commandBody)
	if err != nil {
		onBadRequest(w, err)
		return
	}

	fmt.Printf("httpTohttpTest - target app: %s\n", commandBody.RemoteApp)

	daprAddress := fmt.Sprintf("localhost:%s", strconv.Itoa(daprPort))

	fmt.Printf("dapr address is %s\n", daprAddress)
	testMessage := guuid.New().String()
	b, err := json.Marshal(testMessage)
	if err != nil {
		fmt.Printf("marshal had error %s\n", err)
		onSerializationFailed(w, err)
		return
	}

	fmt.Printf("httpTohttpTest calling with message %s\n", string(b))
	headers := map[string]string{
		"DaprTest-Request-1": "DaprValue1",
		"DaprTest-Request-2": "DaprValue2",
	}

	tracing, _ := strconv.ParseBool(commandBody.RemoteAppTracing)
	if tracing {
		headers["Daprtest-Traceid"] = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	}

	resp, err := send(commandBody.RemoteApp, "retrieve_request_object", b, headers)

	if err != nil {
		fmt.Printf("response had error %s\n", err)
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

	fmt.Printf("response was %s\n", respBody)

	logAndSetResponse(w, http.StatusOK, string(respBody))
}

// testDaprIDRequestHTTPToHTTP calls from http caller to http callee without requiring the caller to use Dapr style URL.
func testDaprIDRequestHTTPToHTTP(w http.ResponseWriter, r *http.Request) {
	fmt.Println("Enter service invocation with dapr-app-id header and shorter URL - http -> http")
	requestHTTPToHTTP(w, r, invokeServiceWithDaprAppIDHeader)
}

// testV1RequestHTTPToHTTP calls from http caller to http callee
func testV1RequestHTTPToHTTP(w http.ResponseWriter, r *http.Request) {
	fmt.Println("Enter service invocation v1 - http -> http")
	requestHTTPToHTTP(w, r, invokeServiceWithBodyHeader)
}

// testV1RequestHTTPToGRPC calls from http caller to grpc callee
func testV1RequestHTTPToGRPC(w http.ResponseWriter, r *http.Request) {
	fmt.Println("Enter service invocation v1 - http -> grpc")
	var commandBody testCommandRequest
	err := json.NewDecoder(r.Body).Decode(&commandBody)
	if err != nil {
		onBadRequest(w, err)
		return
	}

	fmt.Printf("httpTogrpcTest - target app: %s\n", commandBody.RemoteApp)

	daprAddress := fmt.Sprintf("localhost:%s", strconv.Itoa(daprPort))

	fmt.Printf("dapr address is %s\n", daprAddress)
	testMessage := guuid.New().String()
	b, err := json.Marshal(testMessage)
	if err != nil {
		fmt.Printf("marshal had error %s\n", err)
		onSerializationFailed(w, err)
		return
	}

	fmt.Printf("httpTogrpcTest calling with message %s\n", string(b))
	headers := map[string]string{
		"DaprTest-Request-1": "DaprValue1",
		"DaprTest-Request-2": "DaprValue2",
	}

	tracing, _ := strconv.ParseBool(commandBody.RemoteAppTracing)
	if tracing {
		headers["Daprtest-Traceid"] = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	}
	resp, err := invokeServiceWithBodyHeader(
		commandBody.RemoteApp,
		"retrieve_request_object",
		b,
		headers,
	)

	if err != nil {
		fmt.Printf("response had error %s\n", err)
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

	fmt.Printf("response was %s\n", respBody)

	logAndSetResponse(w, http.StatusOK, string(respBody))
}

// testV1RequestGRPCToGRPC calls from http caller to grpc callee
func testV1RequestGRPCToGRPC(w http.ResponseWriter, r *http.Request) {
	fmt.Println("Enter service invocation v1 - grpc -> grpc")
	var commandBody testCommandRequest
	err := json.NewDecoder(r.Body).Decode(&commandBody)
	if err != nil {
		onBadRequest(w, err)
		return
	}

	fmt.Printf("gRPCTogRPCTest - target app: %s\n", commandBody.RemoteApp)

	daprAddress := fmt.Sprintf("localhost:%s", "50001")

	fmt.Printf("dapr address is %s\n", daprAddress)
	conn, err := grpc.Dial(daprAddress, grpc.WithInsecure())
	if err != nil {
		fmt.Println(err)
	}
	defer conn.Close()

	// Create the client
	tracing, _ := strconv.ParseBool(commandBody.RemoteAppTracing)
	var ctx context.Context
	if tracing {
		ctx = metadata.AppendToOutgoingContext(
			context.Background(),
			"DaprTest-Request-1", "DaprValue1",
			"DaprTest-Request-2", "DaprValue2",
			"Daprtest-Traceid", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
		)
	} else {
		ctx = metadata.AppendToOutgoingContext(
			context.Background(),
			"DaprTest-Request-1", "DaprValue1",
			"DaprTest-Request-2", "DaprValue2",
		)
	}
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
		fmt.Printf("response had error %s\n", err)
		onHTTPCallFailed(w, 0, err)
		return
	}

	reqHeadersString := resp.GetData().Value

	respHeaders := map[string][]string{}
	for k, vals := range header {
		var listValue []string
		if strings.HasSuffix(k, "-bin") {
			for _, val := range vals {
				listValue = append(listValue, base64.StdEncoding.EncodeToString([]byte(val)))
			}
		} else {
			listValue = append(listValue, vals...)
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

	fmt.Printf("response was %s\n", respBody)

	logAndSetResponse(w, http.StatusOK, string(respBody))
}

// testV1RequestGRPCToHTTP calls from grpc caller to http callee
func testV1RequestGRPCToHTTP(w http.ResponseWriter, r *http.Request) {
	fmt.Println("Enter service invocation v1 - grpc -> http")
	var commandBody testCommandRequest
	err := json.NewDecoder(r.Body).Decode(&commandBody)
	if err != nil {
		onBadRequest(w, err)
		return
	}

	fmt.Printf("grpcToHTTPTest - target app: %s\n", commandBody.RemoteApp)

	tracing, _ := strconv.ParseBool(commandBody.RemoteAppTracing)
	var ctx context.Context
	if tracing {
		ctx = metadata.AppendToOutgoingContext(
			context.Background(),
			"DaprTest-Request-1", "DaprValue1",
			"DaprTest-Request-2", "DaprValue2",
			"Daprtest-Traceid", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
		)
	} else {
		ctx = metadata.AppendToOutgoingContext(
			context.Background(),
			"DaprTest-Request-1", "DaprValue1",
			"DaprTest-Request-2", "DaprValue2",
		)
	}

	req := &runtimev1pb.InvokeServiceRequest{
		Id: commandBody.RemoteApp,
		Message: &commonv1pb.InvokeRequest{
			Method:      "retrieve_request_object",
			Data:        &anypb.Any{Value: []byte("GRPCToHTTPTest")},
			ContentType: "text/plain; utf-8",
			HttpExtension: &commonv1pb.HTTPExtension{
				Verb: commonv1pb.HTTPExtension_POST,
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
		fmt.Printf("response had error %s\n", err)
		onHTTPCallFailed(w, 0, err)
		return
	}

	reqHeadersString := resp.GetData().Value

	respHeaders := map[string][]string{}
	for k, vals := range header {
		var listValue []string
		if strings.HasSuffix(k, "-bin") {
			for _, val := range vals {
				listValue = append(listValue, base64.StdEncoding.EncodeToString([]byte(val)))
			}
		} else {
			listValue = append(listValue, vals...)
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

	fmt.Printf("response was %s\n", respBody)

	logAndSetResponse(w, http.StatusOK, string(respBody))
}

// Performs calls from grpc client to grpc server.  It sends a random string to the other app
// and expects the response to contain the same string inside an appResponse.
func grpcToGrpcTest(w http.ResponseWriter, r *http.Request) {
	fmt.Println("Enter grpcToGrpcTest")
	var commandBody testCommandRequest
	err := json.NewDecoder(r.Body).Decode(&commandBody)
	if err != nil {
		onBadRequest(w, err)
		return
	}

	fmt.Printf("grpcToGrpcTest - target app: %s\n", commandBody.RemoteApp)

	testMessage := guuid.New().String()
	b, err := json.Marshal(testMessage)
	if err != nil {
		fmt.Printf("marshal had error %s\n", err)
		onSerializationFailed(w, err)
		return
	}

	fmt.Printf("grpcToGrpcTest calling with message %s\n", string(b))

	var req = constructRequest(commandBody.RemoteApp, "grpcToGrpcTest", "", b)
	resp, err := daprClient.InvokeService(context.Background(), req)

	if err != nil {
		logAndSetResponse(w, http.StatusInternalServerError, "grpc call failed with "+err.Error())
		return
	}

	body := resp.Data.GetValue()
	fmt.Printf("resp was %s\n", string(body))

	var responseMessage appResponse
	err = json.Unmarshal(body, &responseMessage)
	if err != nil {
		onDeserializationFailed(w, err)
		return
	}

	// validate response ends with "[testMessage] | [httpMethod]"
	if testMessage != responseMessage.Message {
		errorMessage := "Expected " + testMessage + " received " + responseMessage.Message
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(appResponse{
			Message: errorMessage,
		})
		logAndSetResponse(w, http.StatusInternalServerError, errorMessage)
		return
	}

	// caller of this method doesn't inspect response, it only looks for status
	logAndSetResponse(w, http.StatusOK, "success")
}

// Performs calls from http client to grpc server.  It sends a random string to the other app
// and expects the response to contain the same string inside an appResponse.
func httpToGrpcTest(w http.ResponseWriter, r *http.Request) {
	fmt.Println("Enter httpToGrpcTest")
	var commandBody testCommandRequest
	err := json.NewDecoder(r.Body).Decode(&commandBody)
	if err != nil {
		onBadRequest(w, err)
		return
	}

	fmt.Printf("httpToGrpcTest - target app: %s\n", commandBody.RemoteApp)

	daprAddress := fmt.Sprintf("localhost:%s", strconv.Itoa(daprPort))

	fmt.Printf("dapr address is %s\n", daprAddress)
	testMessage := guuid.New().String()
	b, err := json.Marshal(testMessage)
	if err != nil {
		fmt.Printf("marshal had error %s\n", err)
		onSerializationFailed(w, err)
		return
	}

	fmt.Printf("httpToGrpcTest calling with message %s\n", string(b))
	resp, statusCode, err := invokeServiceWithBody(commandBody.RemoteApp, "httpToGrpcTest", b)

	if err != nil {
		fmt.Printf("response had error %s\n", err)
		onHTTPCallFailed(w, statusCode, err)
		return
	}

	fmt.Printf("response was %s\n", resp.Message)

	logAndSetResponse(w, http.StatusOK, "success")
}

// Performs calls from http client to http server.  It sends a random string to the other app
// and expects the response to contain the same string inside an appResponse.
// It calls methods with all 4 http methods (verbs)
func httpTohttpTest(w http.ResponseWriter, r *http.Request) {
	fmt.Println("Enter httpTohttpTest")
	var commandBody testCommandRequest
	err := json.NewDecoder(r.Body).Decode(&commandBody)
	if err != nil {
		onBadRequest(w, err)
		return
	}

	fmt.Printf("httpTohttpTest - target app: %s\n", commandBody.RemoteApp)

	daprAddress := fmt.Sprintf("localhost:%s", strconv.Itoa(daprPort))

	fmt.Printf("dapr address is %s\n", daprAddress)
	testMessage := guuid.New().String()

	fmt.Printf("httpTohttpTest calling with message %s\n", testMessage)

	for _, test := range testMethods {

		testMessage := "ok"
		if test.SendBody {
			testMessage = guuid.New().String()
		}
		url := fmt.Sprintf(
			"http://localhost:%s/v1.0/invoke/%s/method/%s",
			strconv.Itoa(daprPort), commandBody.RemoteApp,
			test.Callback)
		fmt.Printf("%s invoke url is %s\n", test.Verb, url)
		var b []byte = nil

		if test.SendBody {
			var err error
			b, err = json.Marshal(testMessage)
			if err != nil {
				fmt.Printf("marshal had error %s\n", err)
				onSerializationFailed(w, err)
				return
			}
		}

		resp, err := httpWrapper(test.Verb, url, b)
		if err != nil {
			fmt.Printf("response had error %s\n", err)
			onHTTPCallFailed(w, 0, err)
			return
		}

		if test.ExpectBody && testMessage != resp.Message {
			errorMessage := "Expected " + testMessage + " received " + resp.Message
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(appResponse{
				Message: errorMessage,
			})
			logAndSetResponse(w, http.StatusInternalServerError, errorMessage)
			return
		}

		fmt.Printf("httpTohttpTest - %s test successful\n", test.Verb)
	}

	logAndSetResponse(w, http.StatusOK, "success")
}

// data should be serialized by caller
func httpWrapper(httpMethod string, url string, data []byte) (appResponse, error) {
	client := newHTTPClient()
	var body []byte

	var requestBody io.Reader = nil
	if data != nil && len(data) > 0 {
		requestBody = bytes.NewBuffer(data)
	}

	req, err := http.NewRequest(httpMethod, sanitizeHTTPURL(url), requestBody)
	if err != nil {
		return appResponse{}, err
	}
	var res *http.Response
	res, err = client.Do(req)
	if err != nil {
		return appResponse{}, err
	}

	body, err = extractBody(res.Body)
	defer res.Body.Close()
	if err != nil {
		return appResponse{}, err
	}

	actualVerb := res.Header.Get("x-dapr-tests-request-method")

	if httpMethod != actualVerb {
		return appResponse{}, errors.New(fmt.Sprintf("Expected HTTP verb: %s actual %s", httpMethod, actualVerb))
	}

	var appResp appResponse
	if body != nil && len(body) > 0 {
		err = json.Unmarshal(body, &appResp)
		if err != nil {
			return appResponse{}, err
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

		resp, err = daprClient.InvokeService(context.Background(), req)
		if err != nil {
			logAndSetResponse(w, http.StatusInternalServerError, "error returned from grpc client")
			return
		}

		body := resp.Data.GetValue()

		fmt.Printf("resp was %s\n", string(body))
		//var responseMessage string
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

	daprAddress := fmt.Sprintf("localhost:%s", strconv.Itoa(daprPort))

	fmt.Printf("dapr address is %s\n", daprAddress)
	var testMessage struct {
		Data string `json:"data"`
	}
	testMessage.Data = guuid.New().String()

	fmt.Printf("badServiceCallTestHTTP calling with message %v\n", testMessage)

	// post
	url := fmt.Sprintf(
		"http://localhost:%s/v1.0/invoke/%s/method/%s",
		strconv.Itoa(daprPort), commandBody.RemoteApp,
		commandBody.Method)
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
	resp, err := httpClient.Post(sanitizeHTTPURL(url), jsonContentType, bytes.NewBuffer(b)) // nolint
	if commandBody.Method == "timeouterror" {
		httpClient.Timeout = prevTimeout
	}

	testResponse.MainCallSuccessful = err == nil && resp.StatusCode == 200

	if resp != nil && resp.Body != nil {
		fmt.Printf("badServiceCallTestHTTP - Response Code: %d", resp.StatusCode)
		w.WriteHeader(resp.StatusCode)
		rawBody, _ := extractBody(resp.Body)
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

func badServiceCallTestGrpc(w http.ResponseWriter, r *http.Request) {
	fmt.Println("Enter badServiceCallTestGrpc")
	var commandBody testCommandRequest
	err := json.NewDecoder(r.Body).Decode(&commandBody)
	if err != nil {
		onBadRequest(w, err)
		return
	}

	fmt.Printf("badServiceCallTestGrpc - target app: %s\n", commandBody.RemoteApp)

	timeoutDuration := time.Duration(30)
	// Shorten the timeout if we want to force the error
	if commandBody.Method == "timeouterror" {
		timeoutDuration = time.Duration(5)
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeoutDuration*time.Second)
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

	if resp != nil && resp.Data != nil {
		rawBody := resp.Data.GetValue()
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
		strconv.Itoa(daprPort))

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
			size: 1024*1024*3 - 1,
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
		fmt.Printf("largeDataErrorServiceCall - Request size: %d\n", len(jsonBody))

		if isHTTP {
			resp, err := httpClient.Post(sanitizeHTTPURL(url), jsonContentType, bytes.NewBuffer(jsonBody)) // nolint

			result.CallSuccessful = !((resp != nil && resp.StatusCode != 200) || err != nil)
		} else {
			req := constructRequest("serviceinvocation-callee-0", "posthandler", "POST", jsonBody)

			_, err := daprClient.InvokeService(context.Background(), req)

			result.CallSuccessful = err == nil
		}

		results[i] = result
	}

	json.NewEncoder(w).Encode(results)
}

func main() {
	initGRPCClient()

	log.Printf("Hello Dapr - listening on http://localhost:%d", appPort)

	httpMethods = []string{"POST", "GET", "PUT", "DELETE"}

	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", appPort), appRouter()))
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
	fmt.Println(message)

	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(
		appResponse{Message: message})
}

//--- The functions below are copied from utils/helpers.go to workaround the package include in a container problem.

// HTTPPost is a helper to make POST request call to url
func HTTPPost(url string, data []byte) ([]byte, error) {
	resp, err := httpClient.Post(sanitizeHTTPURL(url), jsonContentType, bytes.NewBuffer(data)) //nolint

	if err != nil {
		return nil, err
	}

	return extractBody(resp.Body)
}

// Wraps GET calls
func HTTPGet(url string) ([]byte, error) {
	resp, err := httpClient.Get(sanitizeHTTPURL(url)) //nolint

	if err != nil {
		return nil, err
	}

	return extractBody(resp.Body)
}

// HTTPDelete calls a given URL with the HTTP DELETE method.
func HTTPDelete(url string, data []byte) ([]byte, error) {
	var requestBody io.Reader = nil
	if data != nil {
		requestBody = bytes.NewBuffer(data)
	}

	req, err := http.NewRequest("DELETE", sanitizeHTTPURL(url), requestBody)
	if err != nil {
		return nil, err
	}

	res, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}

	body, err := extractBody(res.Body)
	defer res.Body.Close()
	if err != nil {
		return nil, err
	}

	return body, nil
}

func HTTPPut(url string, data []byte) ([]byte, error) {
	var requestBody io.Reader = nil
	if data != nil {
		requestBody = bytes.NewBuffer(data)
	}

	req, err := http.NewRequest("PUT", sanitizeHTTPURL(url), requestBody)
	if err != nil {
		return nil, err
	}

	res, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}

	body, err := extractBody(res.Body)
	defer res.Body.Close()
	if err != nil {
		return nil, err
	}

	return body, nil
}

func sanitizeHTTPURL(url string) string {
	if !strings.Contains(url, "http") {
		url = fmt.Sprintf("http://%s", url)
	}

	return url
}

func extractBody(r io.ReadCloser) ([]byte, error) {
	if r != nil {
		defer r.Close()
	}

	body, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}

	return body, nil
}

func initGRPCClient() {
	daprPort, _ := os.LookupEnv("DAPR_GRPC_PORT")
	url := fmt.Sprintf("localhost:%s", daprPort)
	log.Printf("Connecting to dapr using url %s", url)
	for retries := 10; retries > 0; retries-- {
		var err error
		grpcConn, err = grpc.Dial(url, grpc.WithInsecure())
		if err == nil {
			break
		}

		if retries == 0 {
			log.Printf("Could not connect to dapr: %v", err)
			log.Panic(err)
		}

		log.Printf("Could not connect to dapr: %v, retrying...", err)
		time.Sleep(5 * time.Second)
	}

	daprClient = runtimev1pb.NewDaprClient(grpcConn)
}

func newHTTPClient() *http.Client {
	dialer := &net.Dialer{ //nolint:exhaustivestruct
		Timeout: 5 * time.Second,
	}
	netTransport := &http.Transport{ //nolint:exhaustivestruct
		DialContext:         dialer.DialContext,
		TLSHandshakeTimeout: 5 * time.Second,
	}

	return &http.Client{ //nolint:exhaustivestruct
		Timeout:   30 * time.Second,
		Transport: netTransport,
	}
}
