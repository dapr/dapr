// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package main

import (
	"fmt"
	"log"
	"net/http"
)

const appPort = 3000

func rootHandler(w http.ResponseWriter, r *http.Request) {
	log.Println("Received request.")
	fmt.Fprintf(w, "Hello, Dapr")
}

func main() {
	log.Printf("Hello Dapr - listening on http://localhost:%d", appPort)

	http.HandleFunc("/", rootHandler)
	http.ListenAndServe(fmt.Sprintf(":%d", appPort), nil)
}
