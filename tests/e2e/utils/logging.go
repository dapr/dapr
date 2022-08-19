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

package utils

import (
	"io"
	"log"
	"os"
	"path/filepath"
	"time"
)

// SetupLogs sets up the target for the built-in "log" package.
// Logs are always written to STDOUT.
// If the DAPR_TEST_LOG_PATH environmental variable is set, logs are also written to a file in that folder.
func SetupLogs(testName string) {
	logPath := os.Getenv("DAPR_TEST_LOG_PATH")
	if logPath != "" {
		err := os.MkdirAll(logPath, os.ModePerm)
		if err != nil {
			log.Printf("Failed to create output log directory '%s' Error was: '%s'", logPath, err)
			return
		}

		date := time.Now().Format("20060102_150405")
		target := filepath.Join(logPath, testName+"_"+date+".log")
		logFile, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_RDWR, 0o666) //nolint:nosnakecase
		if err != nil {
			log.Printf("Failed to open log file '%s' Error was: '%s'", target, err)
			return
		}
		log.Printf("Saving test %s logs to %s", testName, target)
		log.SetOutput(io.MultiWriter(os.Stdout, logFile))
	}
}
