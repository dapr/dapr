/*
Copyright 2023 The Dapr Authors
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
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"

	"github.com/gorilla/mux"

	"github.com/dapr/dapr/tests/apps/utils"
)

const (
	jsonContentType = "application/json"
	tlsCertEnvKey   = "DAPR_TESTS_TLS_CERT"
	tlsKeyEnvKey    = "DAPR_TESTS_TLS_KEY"
)

type httpTestMethods struct {
	Verb       string
	SendBody   bool
	ExpectBody bool
}

var testMethods = []httpTestMethods{
	{
		Verb:       "GET",
		SendBody:   false,
		ExpectBody: true,
	},
	{
		Verb:       "HEAD",
		SendBody:   false,
		ExpectBody: false,
	},
	{
		Verb:       "POST",
		SendBody:   true,
		ExpectBody: true,
	},
	{
		Verb:       "PUT",
		SendBody:   true,
		ExpectBody: true,
	},
	{
		Verb:       "DELETE",
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
		SendBody:   true,
		ExpectBody: true,
	},
	{
		Verb:       "TRACE",
		SendBody:   true,
		ExpectBody: true,
	},
	{
		Verb:       "PATCH",
		SendBody:   true,
		ExpectBody: true,
	},
}

var appPort = 3000
var securedAppPort = 3001

type appResponse struct {
	Message string `json:"message,omitempty"`
}

func init() {
	p := os.Getenv("PORT")
	if p != "" && p != "0" {
		appPort, _ = strconv.Atoi(p)
	}
}

func main() {
	log.Printf("service_invocation_external - listening on http://localhost:%d", appPort)

	go func() {
		os.WriteFile("/tmp/testdata/certs/cert.pem", []byte("-----BEGIN CERTIFICATE-----\nMIIEKzCCAxOgAwIBAgIUfrJC0E+9stht6OaH3yRkFfrEc3kwDQYJKoZIhvcNAQEL\nBQAwgZAxCzAJBgNVBAYTAlVTMRMwEQYDVQQIDApDYWxpZm9ybmlhMRYwFAYDVQQH\nDA1Nb3VudGFpbiBWaWV3MRowGAYDVQQKDBFZb3VyIE9yZ2FuaXphdGlvbjESMBAG\nA1UECwwJWW91ciBVbml0MSQwIgYDVQQDDBtzZXJ2aWNlLWludm9jYXRpb24tZXh0\nZXJuYWwwHhcNMjMwNzE3MDA1MTMyWhcNMzMwNzE0MDA1MTMyWjCBkDELMAkGA1UE\nBhMCVVMxEzARBgNVBAgMCkNhbGlmb3JuaWExFjAUBgNVBAcMDU1vdW50YWluIFZp\nZXcxGjAYBgNVBAoMEVlvdXIgT3JnYW5pemF0aW9uMRIwEAYDVQQLDAlZb3VyIFVu\naXQxJDAiBgNVBAMMG3NlcnZpY2UtaW52b2NhdGlvbi1leHRlcm5hbDCCASIwDQYJ\nKoZIhvcNAQEBBQADggEPADCCAQoCggEBAJ2yPzP14exlaCb3m/ClYtQ1nFUQ7cwT\nhvMCW1iSxJlS4k4nP3/GugUW9u5QTxOhEUGWhqMPTFHuknCbG5NYhgUob7/a0htn\nBLN86b/tQdFrksGVu2fqyRZqiLFs4lw5fSvWzQYTZHTJFagPKjZBMj9LYUmpwMVc\nww0SsAoZ+pp7P34r8Gr1ceGYoBkKUWLXjYq8GxqWLJnwv9mmA7PwXVfBwaBTBNFG\ns141qkpg3OrL0WBLC3IDLkxz8Z8eZohqnl8VaCxWz/LCZlKotJz3HorOFk9DpJHi\nVHePb4A37iruXRe5XLTf+SQUMySF/++NLXrsd0fC5eRSz+94PebbalcCAwEAAaN7\nMHkwHQYDVR0OBBYEFMg3ir3DWfnOvJ2usIUPSfjUBVInMB8GA1UdIwQYMBaAFMg3\nir3DWfnOvJ2usIUPSfjUBVInMA8GA1UdEwEB/wQFMAMBAf8wJgYDVR0RBB8wHYIb\nc2VydmljZS1pbnZvY2F0aW9uLWV4dGVybmFsMA0GCSqGSIb3DQEBCwUAA4IBAQA/\nnV5PPZAEs+1EW1piuLmzVXR12zDJdB4Q71o4wKArwxyFC6BrkpXBRuONDQcz1xgV\nwvQ4Y1+aCD1fiJfC1XcaCWKOaRYU/JYsbwIPOtBOYtULWPLAOnKyJm+Ijszjk+M/\ntXF4qUfskVYjU5M/CVjl8huamPbcAjFfKJ+m835/OqVh8dVzAjHoFTQBh8LOHDxx\nclCJq/IF7AvA1ladaOfzYR3hEOxK+M8oWdRv8Sty2HVRE+um8ttl84Df3OrMFX/j\nyWBXkEO2OGf+UShl0J2R47S7o6ezB+Fcg5w4p4ErayAmOrn8K5FVOqGKQM03GpuB\nS5HQ7AvQoS53ocD3W87o\n-----END CERTIFICATE-----\n"), 0777)
		os.WriteFile("/tmp/testdata/certs/key.pem", []byte("-----BEGIN PRIVATE KEY-----\nMIIEvAIBADANBgkqhkiG9w0BAQEFAASCBKYwggSiAgEAAoIBAQCdsj8z9eHsZWgm\n95vwpWLUNZxVEO3ME4bzAltYksSZUuJOJz9/xroFFvbuUE8ToRFBloajD0xR7pJw\nmxuTWIYFKG+/2tIbZwSzfOm/7UHRa5LBlbtn6skWaoixbOJcOX0r1s0GE2R0yRWo\nDyo2QTI/S2FJqcDFXMMNErAKGfqaez9+K/Bq9XHhmKAZClFi142KvBsaliyZ8L/Z\npgOz8F1XwcGgUwTRRrNeNapKYNzqy9FgSwtyAy5Mc/GfHmaIap5fFWgsVs/ywmZS\nqLSc9x6KzhZPQ6SR4lR3j2+AN+4q7l0XuVy03/kkFDMkhf/vjS167HdHwuXkUs/v\neD3m22pXAgMBAAECggEADiTMkmZ9SuyVeNlAfUb4JDxityZ7jZA6zFTryHfxdib+\n8S+H/Hfge1Bh+WHulb4ehBlyHfHjUH3rWHow8PhBnPjRfQ2tRpPo0ghkFri67IOP\neBlr45glTQNlGzS+4LKvTV1IqkDXA7p9XMNvb8pNLWwfX7H//jOhsux4KY/G8E/1\niQUONGIfbYU2o4ConRKfVd8WklDWx0hlKID4GgNaHcaMFyJwFai6TmPIezX0YVV/\n3KkS5U9Y7IH/Vh5lnzsl8KAwpum7nU04qsCMb9UC/BAEukEdYilk88z3axadhMxu\n6shAqUUG+MiwZRi3Z8mdIGXFin7iZJAb25cFDEMq4QKBgQDJ1H/xgnGV/8gakNmX\nAcuczu33qqLNBnwpFLHaTHlXFvTJ3rkXHfkgai/j+XDPI5zWD+2I23OmwKfwL9tl\naMO7823aP39UJxjEtM6kWiO6TBpx2xsq7LdvjZmpxIlBBRxlNwd0vXlWDaOIC8Bq\nuI/nZH3F5dEj/yneZPvYiNS6bwKBgQDIBV374lD/hfp/wH2MkudWth//UJ5mGoDZ\n+Y8uadcuntg1ydpreZrABk+Do1HNhqT0xi1GCKgre6R3iw9Gj+Vf3FytD34XZ/qN\nfyNai2hkSQYCQNxGSPonfyxrwHi7dVlA/hXW0+A5cCwnJJHSKn0wvx1XCyt4Df11\nx1Vut/7imQKBgBIhO4j3PbrKqi/wuQr+h0rDr86ijlM6dOa/zCYe3nt7PWvwzNGN\nyLYns7uXyZFh0pHMBntXszDUd4F02xFl8scsW77SG1700B3DXBiZXEI82ddGZk9Z\nyjSEFLySQf4MNnvFEK9k4cRgH0N2QseTYqHXfhDLzxibsCghDBpsWCeTAoGAU9n8\nJmYf8p7l/rxhZZcIKE+9ZjkTzlX6AmceYSS5sq+gkBwI1tlpyGpc1yCWYIS7jmmt\n/RgP9/qjXgIL61bTn7Yvm9VM1N8gKEVOOt0KbFA4lqU+MrDRPdGQe7N6aYuuWRKr\ncR3td+bQe7BW+TYauV7FHAJwvKmQwdlItVupHLECgYAw7vY/px+nY0gFwjdPhfnz\ncQu+NOqCs5l7ibY5f53D76rZJDkUGm4fT3bejuL64eeNNI3EHRdcQ5ayeYEbuzy+\nwbiAgaYIlsNH0aVIWkzv1hCbOtXEl7o7uGv7FWVjEvhen865dGCeUD9kGpeePEfM\nJgvfq8dv6ZULV+5OFVpOPg==\n-----END PRIVATE KEY-----\n"), 0777)

		os.Setenv(tlsCertEnvKey, "/tmp/testdata/certs/cert.pem")
		os.Setenv(tlsKeyEnvKey, "/tmp/testdata/certs/key.pem")
		utils.StartServer(securedAppPort, appRouter, true, true)
	}()

	utils.StartServer(appPort, appRouter, true, false)
}

// appRouter initializes restful api router
func appRouter() http.Handler {
	router := mux.NewRouter().StrictSlash(true)

	// Log requests and their processing time
	router.Use(utils.LoggerMiddleware)

	router.HandleFunc("/", indexHandler).Methods("GET")
	router.HandleFunc("/retrieve_request_object", retrieveRequestObject).Methods("POST")
	// these are called through dapr service invocation
	for _, test := range testMethods {
		if test.SendBody {
			router.HandleFunc("/externalInvocation", withBodyHandler).Methods(test.Verb)
		} else {
			router.HandleFunc("/externalInvocation", noBodyHandler).Methods(test.Verb)
		}
	}
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

// indexHandler is the handler for root path
func indexHandler(w http.ResponseWriter, r *http.Request) {
	log.Println("indexHandler is called")

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(appResponse{Message: "success"})
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

func logAndSetResponse(w http.ResponseWriter, statusCode int, message string) {
	log.Println(message)

	w.WriteHeader(statusCode)
	json.NewEncoder(w).
		Encode(appResponse{Message: message})
}

// Handles a request with a JSON body.  Extracts s string from the input json and returns in it an appResponse.
func withBodyHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Printf("withBodyHandler called. HTTP Verb: %s\n", r.Method)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		onBadRequest(w, err)
		return
	}
	fmt.Printf("withBodyHandler body: %s\n", string(body))
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

	logAndSetResponse(w, http.StatusOK, "success")
}
