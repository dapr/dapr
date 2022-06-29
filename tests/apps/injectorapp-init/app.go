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

func writeTLSCerts() {
	host := "" // TODO: figure this out
	validFrom := time.Now()
	validFor := time.Hour
	directory := "/tmp/testdata"
	utils.GenerateTLSCertAndKey(host, validFrom, validFor, directory)
}

func writeSecrets() {
	data := []byte(`{"secret-key": "secret-value"}`)
	err := os.WriteFile("/tmp/testdata/secrets.json", data, fs.ModePerm)
	if err != nil {
		log.Printf("failed to write secret file: %v", err)
	} else {
		log.Printf("secret file is written")
	}
}

func main() {
	// used by the injector test to validate volume mount
	writeSecrets()

	// used by the injector test to validate root cert installation feature
	writeTLSCerts()
}
