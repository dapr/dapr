package client

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// Result is a watch result
type Result struct {
	Type   string
	Object interface{}
}

const Added = "ADDED"
const Modified = "MODIFIED"
const Deleted = "DELETED"

// WatchClient is a client for Watching the Kubernetes API
type WatchClient struct {
	Cfg     *Configuration
	Client  *APIClient
	Path    string
	MakerFn func() interface{}
}

// Connect initiates a watch to the server.
func (w *WatchClient) Connect(ctx context.Context, resourceVersion string) (<-chan *Result, <-chan error, error) {
	params := []string{"watch=true"}
	if len(resourceVersion) != 0 {
		params = append(params, "resourceVersion="+resourceVersion)
	}
	queryStr := "?" + strings.Join(params, "&")
	url := w.Cfg.Scheme + "://" + w.Cfg.Host + w.Path + queryStr
	req, err := w.Client.prepareRequest(ctx, url, "GET", nil, nil, nil, nil, "", []byte{})
	if err != nil {
		return nil, nil, err
	}
	res, err := w.Client.callAPI(req)
	if err != nil {
		return nil, nil, err
	}
	if res.StatusCode != 200 {
		return nil, nil, fmt.Errorf("Error connecting watch (%d: %s)", res.StatusCode, res.Status)
	}
	resultChan := make(chan *Result, 1)
	errChan := make(chan error, 1)
	processWatch(res.Body, w.MakerFn, resultChan, errChan)
	return resultChan, errChan, nil
}

func processWatch(stream io.Reader, makerFn func() interface{}, resultChan chan<- *Result, errChan chan<- error) {
	scanner := bufio.NewScanner(stream)
	go func() {
		defer close(resultChan)
		defer close(errChan)
		for scanner.Scan() {
			watchObj, err := decode(scanner.Text(), makerFn)
			if err != nil {
				errChan <- err
				return
			}
			if watchObj != nil {
				resultChan <- watchObj
			}
		}
		if err := scanner.Err(); err != nil {
			errChan <- err
		}
	}()
}

func decode(line string, makerFn func() interface{}) (*Result, error) {
	if len(line) == 0 {
		return nil, nil
	}
	// TODO: support protocol buffer encoding?
	decoder := json.NewDecoder(strings.NewReader(line))
	result := &Result{}
	for decoder.More() {
		name, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		if name == "type" {
			token, err := decoder.Token()
			if err != nil {
				return nil, err
			}
			var ok bool
			result.Type, ok = token.(string)
			if !ok {
				return nil, fmt.Errorf("Error casting %v to string", token)
			}
		}
		if name == "object" {
			obj := makerFn()
			if err := decoder.Decode(&obj); err != nil {
				return nil, err
			}
			result.Object = obj
			return result, nil
		}
	}
	return nil, nil
}
