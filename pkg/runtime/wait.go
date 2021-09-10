// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package runtime

import (
	"context"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"time"

	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	"github.com/dapr/dapr/pkg/modes"
	operatorv1pb "github.com/dapr/dapr/pkg/proto/operator/v1"
)

var (
	timeoutSeconds       int    = 60
	requestTimeoutMillis int    = 500
	periodMillis         int    = 100
	urlFormat            string = "http://localhost:%s/v1.0/healthz/outbound"
)

func waitUntilDaprOutboundReady(daprHTTPPort string) {
	outboundReadyHealthURL := fmt.Sprintf(urlFormat, daprHTTPPort)
	client := &http.Client{
		Timeout: time.Duration(requestTimeoutMillis) * time.Millisecond,
	}
	println(fmt.Sprintf("Waiting for Dapr to be outbound ready (timeout: %d seconds): url=%s\n", timeoutSeconds, outboundReadyHealthURL))

	var err error
	timeoutAt := time.Now().Add(time.Duration(timeoutSeconds) * time.Second)
	lastPrintErrorTime := time.Now()
	for time.Now().Before(timeoutAt) {
		err = checkIfOutboundReady(client, outboundReadyHealthURL)
		if err == nil {
			println("Dapr is outbound ready!")
			return
		}

		if time.Now().After(lastPrintErrorTime) {
			// print the error once in one seconds to avoid too many errors
			lastPrintErrorTime = time.Now().Add(time.Second)
			println(fmt.Sprintf("Dapr outbound NOT ready yet: %v", err))
		}

		time.Sleep(time.Duration(periodMillis) * time.Millisecond)
	}

	println(fmt.Sprintf("timeout waiting for Dapr to become outbound ready. Last error: %v", err))
}

func checkIfOutboundReady(client *http.Client, outboundReadyHealthURL string) error {
	req, err := http.NewRequest(http.MethodGet, outboundReadyHealthURL, nil)
	if err != nil {
		return err
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	_, err = ioutil.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != 204 {
		return fmt.Errorf("HTTP status code %v", resp.StatusCode)
	}

	return nil
}

func (a *DaprRuntime) blockUntilAppPortOpen() {
	if a.runtimeConfig.ApplicationPort <= 0 {
		return
	}

	log.Infof("application protocol: %s. waiting on port %v.  This will block until the app is listening on that port.", string(a.runtimeConfig.ApplicationProtocol), a.runtimeConfig.ApplicationPort)

	for {
		conn, _ := net.DialTimeout("tcp", net.JoinHostPort("localhost", fmt.Sprintf("%v", a.runtimeConfig.ApplicationPort)), time.Millisecond*500)
		if conn != nil {
			conn.Close()
			break
		}
		// prevents overwhelming the OS with open connections
		time.Sleep(time.Millisecond * 50)
	}

	log.Infof("application discovered on port %v", a.runtimeConfig.ApplicationPort)
}

func (a *DaprRuntime) blockUntilAppIsReady() {
	if a.runtimeConfig.Mode == modes.KubernetesMode && len(a.runtimeConfig.WaitingContainers) > 0 {
		log.Infof("going to check containers ready : namespace = %s, podName = %s, containers = %v", a.namespace, a.podName, a.runtimeConfig.WaitingContainers)
	out:
		for {
			// check readiness from k8s env
			resp, err := a.operatorClient.GetContainersStatus(context.Background(), &operatorv1pb.GetContainersStatusRequest{
				Namespace: a.namespace,
				Name:      a.podName,
			})
			if err != nil {
				log.Errorf("get containers status error : namespace = %s, podName = %s, err = %s", a.namespace, a.podName, err)
				return
			}

			if contains(a.runtimeConfig.WaitingContainers, "all") {
				// all containers in pod should be ready
				for name, ready := range resp.Statuses {
					if name == "daprd" {
						continue
					}
					if !ready {
						log.Infof("container[%s] not ready, continue waiting", name)
						time.Sleep(time.Millisecond * 1000)
						continue out
					}
				}
			} else {
				// all specified containers should be ready
				for _, name := range a.runtimeConfig.WaitingContainers {
					ready, ok := resp.Statuses[name]
					if !ok {
						log.Infof("container[%s] not found, continue waiting", name)
						time.Sleep(time.Millisecond * 1000)
						continue out
					}
					if !ready {
						log.Infof("container[%s] not ready, continue waiting", name)
						time.Sleep(time.Millisecond * 1000)
						continue out
					}
				}
			}

			log.Infof("all containers are ready")
			break
		}
	}

	if a.runtimeConfig.WaitingProbe != "" {
		for {
			req := invokev1.NewInvokeMethodRequest(a.runtimeConfig.WaitingProbe)
			req.WithHTTPExtension(http.MethodGet, "")
			req.WithRawData(nil, invokev1.JSONContentType)

			ctx := context.Background()
			resp, err := a.appChannel.InvokeMethod(ctx, req)

			if err == nil && resp.Status().Code == http.StatusOK {
				log.Infof("application check pass on address %s", a.runtimeConfig.WaitingProbe)
				break
			} else {
				log.Infof("probe[%s] not ready, continue waiting", a.runtimeConfig.WaitingProbe)
			}

			time.Sleep(time.Millisecond * 1000)
		}
	}
	log.Infof("wait application ready, continue init")
}

func contains(source []string, target string) bool {
	if len(source) == 0 {
		return false
	}

	for _, item := range source {
		if target == item {
			return true
		}
	}

	return false
}
