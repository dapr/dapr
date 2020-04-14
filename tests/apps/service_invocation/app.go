// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	guuid "github.com/google/uuid"
	"github.com/gorilla/mux"

	"github.com/dapr/dapr/pkg/proto/dapr"

	"github.com/golang/protobuf/ptypes/any"
	"google.golang.org/grpc"
)

var httpMethods []string

const (
	appPort  = 3000
	daprPort = 3500
)

type testCommandRequest struct {
	RemoteApp string `json:"remoteApp,omitempty"`
	Method    string `json:"method,omitempty"`
}

type appResponse struct {
	Message string `json:"message,omitempty"`
}

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
	response, err := invokeService("serviceinvocation-callee-1", "singlehop")
	if err != nil {
		onHTTPCallFailed(w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

// Handles a post request.  Extracts s string from the input json and returns in it an appResponse.
func postHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Printf("postHandler called \n")
	var s string
	err := json.NewDecoder(r.Body).Decode(&s)
	if err != nil {
		onBadRequest(w, err)
		return
	}

	w.WriteHeader(http.StatusOK)

	json.NewEncoder(w).Encode(appResponse{Message: s})
}

// Handles a get request.  Returns an appResponse with appResponse.Message "ok", which caller validates.
func getHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Printf("getHandler called \n")

	logAndSetResponse(w, http.StatusOK, "ok")
}

// Handles a put request.  Extracts s string from the input json and returns in it an appResponse.
func putHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Printf("putHandler called \n")
	var s string
	err := json.NewDecoder(r.Body).Decode(&s)
	if err != nil {
		onBadRequest(w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(appResponse{Message: s})
}

// Handles a delete request.  Extracts s string from the input json and returns in it an appResponse.
func deleteHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Printf("deleteHandler called \n")
	var s string
	err := json.NewDecoder(r.Body).Decode(&s)
	if err != nil {
		onBadRequest(w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(appResponse{Message: s})
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
	response, err := invokeService(commandBody.RemoteApp, commandBody.Method)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(appResponse{
			Message: err.Error(),
		})
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

func invokeService(remoteApp, method string) (appResponse, error) {
	return invokeServiceWithBody(remoteApp, method, nil)
}

// invoke another service
func invokeServiceWithBody(remoteApp, method string, data []byte) (appResponse, error) {
	url := fmt.Sprintf("http://localhost:%s/v1.0/invoke/%s/method/%s", strconv.Itoa(daprPort), remoteApp, method)
	fmt.Printf("invoke url is %s", url)

	var t io.Reader = nil
	if data != nil {
		t = bytes.NewBuffer(data)
	}

	/* #nosec */
	resp, err := http.Post(url, "application/json", t)

	if err != nil {
		return appResponse{}, err
	}

	body, err := ioutil.ReadAll(resp.Body)
	defer resp.Body.Close()
	if err != nil {
		return appResponse{}, err
	}

	fmt.Printf("invokeService here\n")
	var appResp appResponse
	err = json.Unmarshal(body, &appResp)
	if err != nil {
		return appResponse{}, err
	}

	return appResp, nil
}

// appRouter initializes restful api router
func appRouter() *mux.Router {
	router := mux.NewRouter().StrictSlash(true)

	router.HandleFunc("/", indexHandler).Methods("GET")
	router.HandleFunc("/singlehop", singlehopHandler).Methods("POST")
	router.HandleFunc("/multihop", multihopHandler).Methods("POST")

	router.HandleFunc("/tests/invoke_test", testHandler)

	// these are called through dapr service invocation
	router.HandleFunc("/posthandler", postHandler).Methods("POST")
	router.HandleFunc("/gethandler", getHandler).Methods("GET")
	router.HandleFunc("/puthandler", putHandler).Methods("PUT")
	router.HandleFunc("/deletehandler", deleteHandler).Methods("DELETE")

	// called by test to run some cases - these will themselves make calls
	router.HandleFunc("/httptohttptest", httpTohttpTest).Methods("POST")
	router.HandleFunc("/grpctogrpctest", grpcToGrpcTest).Methods("POST")
	router.HandleFunc("/httptogrpctest", httpToGrpcTest).Methods("POST")
	router.HandleFunc("/grpctohttptest", grpcToHTTPTest).Methods("POST")

	router.Use(mux.CORSMethodMiddleware(router))

	return router
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

	daprPort := 50001
	daprAddress := fmt.Sprintf("localhost:%s", strconv.Itoa(daprPort))

	fmt.Printf("dapr address is %s\n", daprAddress)
	conn, err := grpc.Dial(daprAddress, grpc.WithInsecure())

	if err != nil {
		fmt.Println(err)
	}
	defer conn.Close()

	// Create the client
	client := dapr.NewDaprClient(conn)

	testMessage := guuid.New().String()
	b, err := json.Marshal(testMessage)
	if err != nil {
		fmt.Printf("marshal had error %s\n", err)
		onSerializationFailed(w, err)
		return
	}

	fmt.Printf("grpcToGrpcTest calling with message %s\n", string(b))
	resp, err := client.InvokeService(context.Background(), &dapr.InvokeServiceEnvelope{
		Id:     commandBody.RemoteApp,
		Data:   &any.Any{Value: b},
		Method: "grpcToGrpcTest",
	})

	if err != nil {
		logAndSetResponse(w, http.StatusInternalServerError, "grpc call failed with "+err.Error())
		return
	}

	fmt.Printf("resp was %s\n", string(resp.Data.Value))

	var responseMessage appResponse
	err = json.Unmarshal(resp.Data.Value, &responseMessage)
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
	resp, err := invokeServiceWithBody(commandBody.RemoteApp, "httpToGrpcTest", b)
	if err != nil {
		fmt.Printf("response had error %s\n", err)
		onHTTPCallFailed(w, err)
		return
	}

	fmt.Printf("response was %s\n", resp.Message)

	logAndSetResponse(w, http.StatusOK, "success")
}

// Performs calls from http client to http server.  It sends a random string to the other app
// and expects the response to contain the same string inside an appResponse.
// It calls methods with all 4 http methods (verbs)
func httpTohttpTest(w http.ResponseWriter, r *http.Request) {
	fmt.Println("Enter httpToGrpcTest")
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

	// post
	testMessage = guuid.New().String()
	url := fmt.Sprintf(
		"http://localhost:%s/v1.0/invoke/%s/method/%s",
		strconv.Itoa(daprPort), commandBody.RemoteApp,
		"posthandler")
	fmt.Printf("post invoke url is %s", url)
	b, err := json.Marshal(testMessage)
	if err != nil {
		fmt.Printf("marshal had error %s\n", err)
		onSerializationFailed(w, err)
		return
	}

	resp, err := httpWrapper("POST", url, b)
	if err != nil {
		fmt.Printf("response had error %s\n", err)
		onHTTPCallFailed(w, err)
		return
	}

	if testMessage != resp.Message {
		errorMessage := "Expected " + testMessage + " received " + resp.Message
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(appResponse{
			Message: errorMessage,
		})
		logAndSetResponse(w, http.StatusInternalServerError, errorMessage)
		return
	}

	fmt.Println("httpTohttpTest - post test successful")

	// get
	testMessage = guuid.New().String()
	url = fmt.Sprintf(
		"http://localhost:%s/v1.0/invoke/%s/method/%s",
		strconv.Itoa(daprPort), commandBody.RemoteApp,
		"gethandler")
	fmt.Printf("get invoke url is %s", url)
	b, err = json.Marshal(testMessage)
	if err != nil {
		fmt.Printf("marshal had error %s\n", err)
		onSerializationFailed(w, err)
		return
	}

	resp, err = httpWrapper("GET", url, b)
	if err != nil {
		fmt.Printf("response had error %s\n", err)
		onHTTPCallFailed(w, err)
		return
	}

	// no check, body wasn't sent
	if resp.Message != "ok" {
		errorMessage := "Expected " + "ok" + " received " + resp.Message
		logAndSetResponse(w, http.StatusInternalServerError, errorMessage)
		return
	}

	fmt.Println("httpTohttpTest - get test successful")

	// put
	url = fmt.Sprintf(
		"http://localhost:%s/v1.0/invoke/%s/method/%s",
		strconv.Itoa(daprPort), commandBody.RemoteApp,
		"puthandler")
	fmt.Printf("put invoke url is %s", url)
	b, err = json.Marshal(testMessage)
	if err != nil {
		fmt.Printf("marshal had error %s\n", err)
		onSerializationFailed(w, err)
		return
	}

	resp, err = httpWrapper("PUT", url, b)
	if err != nil {
		fmt.Printf("response had error %s\n", err)
		onHTTPCallFailed(w, err)
		return
	}

	if testMessage != resp.Message {
		errorMessage := "Expected " + testMessage + " received " + resp.Message
		logAndSetResponse(w, http.StatusInternalServerError, errorMessage)
		return
	}

	fmt.Println("httpTohttpTest - put test successful")

	// delete
	testMessage = guuid.New().String()
	url = fmt.Sprintf(
		"http://localhost:%s/v1.0/invoke/%s/method/%s",
		strconv.Itoa(daprPort), commandBody.RemoteApp,
		"deletehandler")
	fmt.Printf("delete invoke url is %s", url)
	b, err = json.Marshal(testMessage)
	if err != nil {
		fmt.Printf("marshal had error %s\n", err)
		onSerializationFailed(w, err)
		return
	}

	resp, err = httpWrapper("DELETE", url, b)
	if err != nil {
		fmt.Printf("response had error %s\n", err)
		onHTTPCallFailed(w, err)
		return
	}

	if testMessage != resp.Message {
		errorMessage := "Expected " + testMessage + " received " + resp.Message
		logAndSetResponse(w, http.StatusInternalServerError, errorMessage)
		return
	}

	fmt.Println("httpTohttpTest - delete test successful")

	logAndSetResponse(w, http.StatusOK, "success")
}

// data should be serialized by caller
func httpWrapper(httpMethod string, url string, data []byte) (appResponse, error) {
	var body []byte
	var err error

	if httpMethod == "POST" {
		body, err = HTTPPost(url, data)
	} else if httpMethod == "GET" {
		body, err = HTTPGet(url)
	} else if httpMethod == "PUT" {
		body, err = HTTPPut(url, data)
	} else if httpMethod == "DELETE" {
		body, err = HTTPDelete(url, data)
	} else {
		return appResponse{}, errors.New("expected option")
	}

	if err != nil {
		return appResponse{}, err
	}

	var appResp appResponse
	err = json.Unmarshal(body, &appResp)
	if err != nil {
		return appResponse{}, err
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

	daprAddress := fmt.Sprintf("localhost:%s", "50001")

	fmt.Printf("dapr address is %s\n", daprAddress)
	conn, err := grpc.Dial(daprAddress, grpc.WithInsecure())
	if err != nil {
		fmt.Println(err)
	}
	defer conn.Close()

	// Create the client
	client := dapr.NewDaprClient(conn)

	metadata := make(map[string]string)

	var b []byte
	for _, v := range httpMethods {
		metadata["http.verb"] = v
		testMessage := guuid.New().String()
		b, err = json.Marshal(testMessage)
		if err != nil {
			fmt.Printf("marshal had error %s\n", err)
			onSerializationFailed(w, err)
			return
		}

		fmt.Printf("grpcToHTTPTest calling with verb %s, message %s\n", v, testMessage)

		envelope := dapr.InvokeServiceEnvelope{
			Id:       commandBody.RemoteApp,
			Data:     &any.Any{Value: b},
			Metadata: metadata,
		}

		var resp *dapr.InvokeServiceResponseEnvelope
		var err error
		switch v {
		case "POST":
			envelope.Method = "posthandler"
		case "GET":
			envelope.Method = "gethandler"
		case "PUT":
			envelope.Method = "puthandler"
		case "DELETE":
			envelope.Method = "deletehandler"
		default:
			fmt.Println("Unexpected option")
			if err != nil {
				logAndSetResponse(w, http.StatusInternalServerError, "unexpected option "+v)
				return
			}
		}

		resp, err = client.InvokeService(context.Background(), &envelope)
		if err != nil {
			logAndSetResponse(w, http.StatusInternalServerError, "error returned from grpc client")
			return
		}

		fmt.Printf("resp was %s\n", string(resp.Data.Value))
		//var responseMessage string
		var appResp appResponse
		err = json.Unmarshal(resp.Data.Value, &appResp)
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

func main() {
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

func onHTTPCallFailed(w http.ResponseWriter, err error) {
	msg := "HTTP call failed with " + err.Error()
	logAndSetResponse(w, http.StatusInternalServerError, msg)
}

func logAndSetResponse(w http.ResponseWriter, statusCode int, message string) {
	fmt.Println(message)

	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(
		appResponse{Message: message})
}

//--- The functions below are copied from utils/helpers.go to workaround the package include in a container problem.

func newHTTPClient() http.Client {
	return http.Client{
		Transport: &http.Transport{
			// Sometimes, the first connection to ingress endpoint takes longer than 1 minute (e.g. AKS)
			Dial: (&net.Dialer{
				Timeout: 5 * time.Minute,
			}).Dial,
		},
	}
}

// HTTPPost is a helper to make POST request call to url
func HTTPPost(url string, data []byte) ([]byte, error) {
	client := newHTTPClient()
	resp, err := client.Post(sanitizeHTTPURL(url), "application/json", bytes.NewBuffer(data)) //nolint

	if err != nil {
		return nil, err
	}

	return extractBody(resp.Body)
}

// Wraps GET calls
func HTTPGet(url string) ([]byte, error) {
	client := newHTTPClient()
	resp, err := client.Get(sanitizeHTTPURL(url)) //nolint

	if err != nil {
		return nil, err
	}

	return extractBody(resp.Body)
}

// HTTPDelete calls a given URL with the HTTP DELETE method.
func HTTPDelete(url string, data []byte) ([]byte, error) {
	client := newHTTPClient()

	var requestBody io.Reader = nil
	if data != nil {
		requestBody = bytes.NewBuffer(data)
	}

	req, err := http.NewRequest("DELETE", sanitizeHTTPURL(url), requestBody)
	if err != nil {
		return nil, err
	}

	res, err := client.Do(req)
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

// sends put
func HTTPPut(url string, data []byte) ([]byte, error) {
	client := newHTTPClient()

	var requestBody io.Reader = nil
	if data != nil {
		requestBody = bytes.NewBuffer(data)
	}

	req, err := http.NewRequest("PUT", sanitizeHTTPURL(url), requestBody)
	if err != nil {
		return nil, err
	}

	res, err := client.Do(req)
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

	body, err := ioutil.ReadAll(r)
	if err != nil {
		return nil, err
	}

	return body, nil
}
