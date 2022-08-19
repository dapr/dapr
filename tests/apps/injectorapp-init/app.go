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
	"io/fs"
	"log"
	"os"
	"time"

	"github.com/dapr/dapr/tests/apps/utils"
)

//nolint:gosec
func writeSecrets() {
	data := []byte(`{"secret-key": "secret-value"}`)
	err := os.WriteFile("/tmp/testdata/secrets.json", data, fs.ModePerm)
	if err != nil {
		log.Printf("failed to write secret file: %v", err)
	} else {
		log.Printf("secret file is written")
	}
}

func writeTLSCertAndKey() {
	host := "localhost"
	validFrom := time.Now()
	validFor := time.Hour
	directory := "/tmp/testdata/certs"

	// ensure that the directory exists
	if _, err := os.Stat(directory); os.IsNotExist(err) {
		err := os.MkdirAll(directory, os.ModePerm)
		if err != nil {
			log.Printf("failed to create directory: %v", err)
		}
	}

	// generate the certs
	err := utils.GenerateTLSCertAndKey(host, validFrom, validFor, directory)
	if err != nil {
		log.Printf("failed to generate TLS cert and key: %v", err)
	} else {
		log.Printf("TLS cert and key is generated")
	}
}

func main() {
	// used by the injector test to validate volume mount
	writeSecrets()

	// used by the injector test to validate root cert installation feature
	writeTLSCertAndKey()
}
