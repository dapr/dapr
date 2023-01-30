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
	"testing"

	"github.com/labstack/gommon/log"
)

type Table struct {
	Heading string      `json:"heading"`
	Data    [][2]string `json:"data"`
}

func (t *Table) Output(header, value string) *Table {
	t.Data = append(t.Data, [2]string{header, value})
	return t
}

func (t *Table) OutputInt(header string, value int) *Table {
	return t.Output(header, strconv.Itoa(value))
}

func (t *Table) Outputf(header, format string, params ...any) *Table {
	return t.Output(header, fmt.Sprintf(format, params...))
}

func (t *Table) Flush() error {
	bts, err := json.Marshal(t)
	if err != nil {
		log.Errorf("error when marshalling table %s %v", t.Heading, err)
		return err
	}

	filePrefixOutput, ok := os.LookupEnv("TEST_OUTPUT_FILE_PREFIX")
	if !ok {
		filePrefixOutput = "./test_report"
	}

	err = os.WriteFile(fmt.Sprintf("%s_summary_table_%s.json", filePrefixOutput, t.Heading), bts, os.ModePerm)
	if err != nil {
		log.Errorf("error when saving table %s %v", t.Heading, err)
		return err
	}
	return nil
}

// ForTest returns a table ready to be written for the given test..
func ForTest(tst *testing.T) *Table {
	return &Table{
		Heading: tst.Name(),
		Data:    [][2]string{},
	}
}
