// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"time"

	"github.com/gorilla/mux"
)

const appPort = 3000

// kubernetes is the name of the secret store
const (
	secretStore      = "kubernetes"
	nonexistentStore = "nonexistent"
	/* #nosec */
	secretURL = "http://localhost:3500/v1.0/secrets/%s/%s?metadata.namespace=dapr-tests"
)

// daprSecret represents a secret in Dapr.
type daprSecret struct {
	Key   string             `json:"key,omitempty"`
	Store string             `json:"store,omitempty"`
	Value *map[string]string `json:"value,omitempty"`
}

// requestResponse represents a request or response for the APIs in this app.
type requestResponse struct {
	StartTime int          `json:"start_time,omitempty"`
	EndTime   int          `json:"end_time,omitempty"`
	Secrets   []daprSecret `json:"secrets,omitempty"`
	Message   string       `json:"message,omitempty"`
}

// indexHandler is the handler for root path
func indexHandler(w http.ResponseWriter, _ *http.Request) {
	log.Println("indexHandler is called")

	w.WriteHeader(http.StatusOK)
}

func get(key, store string) (*map[string]string, int, error) {
	log.Printf("Processing get request for %s.", key)
	url, err := createSecretURL(key, store)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}

	log.Printf("Fetching state from %s", url)
	// url is created from user input, it is OK since this is a test app only and will not run in prod.
	/* #nosec */
	res, err := http.Get(url)
	if err != nil {
		return nil, http.StatusInternalServerError, fmt.Errorf("could not get value for key %s from Dapr: %s", key, err.Error())
	}
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, http.StatusInternalServerError, fmt.Errorf("could not load value for key %s from Dapr: %s", key, err.Error())
	}
	if res.StatusCode != http.StatusOK {
		log.Printf("Non 200 StatusCode: %d\n", res.StatusCode)

		return nil, res.StatusCode, fmt.Errorf("got err response for key %s from Dapr: %s", key, body)
	}

	log.Printf("Found secret for key %s: %s", key, body)

	var state = map[string]string{}
	if len(body) == 0 {
		return nil, http.StatusOK, nil
	}

	// a key not found in Dapr will return 200 but an empty response.
	err = json.Unmarshal(body, &state)
	if err != nil {
		return nil, http.StatusInternalServerError, fmt.Errorf("could not parse value for key %s from Dapr: %s", key, err.Error())
	}

	return &state, http.StatusOK, nil
}

func getAll(secrets []daprSecret) ([]daprSecret, int, error) {
	statusCode := http.StatusOK
	log.Printf("Processing get request for %d states.", len(secrets))

	var output = make([]daprSecret, 0, len(secrets))
	for _, secret := range secrets {
		value, sc, err := get(secret.Key, secret.Store)
		if err != nil {
			return nil, sc, err
		}

		log.Printf("Result for get request for key %s: %v", secret.Key, value)
		output = append(output, daprSecret{
			Key:   secret.Key,
			Value: value,
			Store: secret.Store,
		})
		statusCode = sc
	}

	log.Printf("Result for get request for %d secrets: %v", len(secrets), output)

	return output, statusCode, nil
}

// handles all APIs
func handler(w http.ResponseWriter, r *http.Request) {
	log.Printf("Processing request for %s", r.URL.RequestURI())

	// Retrieve request body contents
	var req requestResponse
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		log.Printf("Could not parse request body: %s", err.Error())
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(requestResponse{
			Message: err.Error(),
		})
		return
	}

	var res = requestResponse{}
	var uri = r.URL.RequestURI()
	var secrets []daprSecret
	var statusCode int

	res.StartTime = epoch()

	cmd := mux.Vars(r)["command"]
	switch cmd {
	case "get":
		secrets, statusCode, err = getAll(req.Secrets)
		res.Secrets = secrets
		if statusCode != http.StatusOK {
			res.Message = err.Error()
		}
	default:
		err = fmt.Errorf("invalid URI: %s", uri)
		statusCode = http.StatusBadRequest
		res.Message = err.Error()
	}

	res.EndTime = epoch()

	if statusCode != http.StatusOK {
		log.Printf("Error status code %v: %v", statusCode, res.Message)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(res)
}

func createSecretURL(key, store string) (string, error) {
	url, err := url.Parse(fmt.Sprintf(secretURL, store, key))
	if err != nil {
		return "", fmt.Errorf("could not parse %s: %s", secretURL, err.Error())
	}

	return url.String(), nil
}

// epoch returns the current unix epoch timestamp
func epoch() int {
	return (int)(time.Now().UTC().UnixNano() / 1000000)
}

// appRouter initializes restful api router
func appRouter() *mux.Router {
	router := mux.NewRouter().StrictSlash(true)

	router.HandleFunc("/", indexHandler).Methods("GET")
	router.HandleFunc("/test/{command}", handler).Methods("POST")

	router.Use(mux.CORSMethodMiddleware(router))

	return router
}

func main() {
	log.Printf("Secret App - listening on http://localhost:%d", appPort)
	log.Printf("Secret endpoint - to be saved at %s", secretURL)

	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", appPort), appRouter()))
}
