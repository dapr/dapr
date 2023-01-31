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

package summary

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/labstack/gommon/log"
)

func filePath(prefix, testName string) string {
	return fmt.Sprintf("%s_summary_table_%s.json", prefix, strings.ReplaceAll(testName, string(os.PathSeparator), "_"))
}

// Table is primarily used as a source of arbitrary test output. Tests can send output to the summary table and later flush them.
// when flush is called so the table is serialized into a file that contains the test name on it, so you should use one table per test.
// the table output is typically used as a github enhanced job summary.
// see more: https://github.blog/2022-05-09-supercharging-github-actions-with-job-summaries/
type Table struct {
	Test string      `json:"test"`
	Data [][2]string `json:"data"`
}

// Output adds a pair to the table data pairs.
func (t *Table) Output(header, value string) *Table {
	t.Data = append(t.Data, [2]string{header, value})
	return t
}

// OutputInt same as output but converts from int to string.
func (t *Table) OutputInt(header string, value int) *Table {
	return t.Output(header, strconv.Itoa(value))
}

// Outputf same as output but uses a formatter.
func (t *Table) Outputf(header, format string, params ...any) *Table {
	return t.Output(header, fmt.Sprintf(format, params...))
}

// Flush saves the summary into the disk using the desired format.
func (t *Table) Flush() error {
	bts, err := json.Marshal(t)
	if err != nil {
		log.Errorf("error when marshalling table %s %v", t.Test, err)
		return err
	}

	filePrefixOutput, ok := os.LookupEnv("TEST_OUTPUT_FILE_PREFIX")
	if !ok {
		filePrefixOutput = "./test_report"
	}

	err = os.WriteFile(filePath(filePrefixOutput, t.Test), bts, os.ModePerm)
	if err != nil {
		log.Errorf("error when saving table %s %v", t.Test, err)
		return err
	}
	return nil
}

// ForTest returns a table ready to be written for the given test.
func ForTest(tst *testing.T) *Table {
	return &Table{
		Test: tst.Name(),
		Data: [][2]string{},
	}
}
