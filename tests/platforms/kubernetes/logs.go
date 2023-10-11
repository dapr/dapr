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

package kubernetes

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1 "k8s.io/client-go/kubernetes/typed/core/v1"
)

var logPrefix string

func init() {
	logPrefix = os.Getenv(ContainerLogPathEnvVar)

	if logPrefix == "" {
		logPrefix = ContainerLogDefaultPath
	}
}

// StreamContainerLogsToDisk streams all containers logs for the given selector to a given disk directory.
func StreamContainerLogsToDisk(ctx context.Context, appName string, podClient v1.PodInterface) error {
	listCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	podList, err := podClient.List(listCtx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=%s", TestAppLabelKey, appName),
	})
	cancel()
	if err != nil {
		return err
	}

	for _, pod := range podList.Items {
		for _, container := range pod.Spec.Containers {
			go func(pod, container string) {
			loop:
				for {
					filename := filepath.Join(logPrefix, fmt.Sprintf("%s.%s.log", pod, container))
					log.Printf("Streaming Kubernetes logs to %s", filename)
					req := podClient.GetLogs(pod, &apiv1.PodLogOptions{
						Container: container,
						Follow:    true,
					})
					stream, err := req.Stream(ctx)
					if err != nil {
						switch {
						case errors.Is(err, context.Canceled):
							log.Printf("Saved container logs to %s", filename)
							return
						case strings.Contains(err.Error(), "ContainerCreating"):
							// Retry after a delay
							time.Sleep(100 * time.Millisecond)
							continue loop
						default:
							log.Printf("Error starting log stream for %s. Error was %v", filename, err)
							return
						}
					}
					defer stream.Close()

					fh, err := os.Create(filename)
					if err != nil {
						log.Printf("Error creating %s. Error was %s", filename, err)
						return
					}
					defer fh.Close()

					_, err = io.Copy(fh, stream)
					if err != nil {
						switch {
						case errors.Is(err, context.Canceled):
							log.Printf("Saved container logs to %s", filename)
							return
						default:
							log.Printf("Error copying log stream for %s. Error was %v", filename, err)
							return
						}
					}

					log.Printf("Saved container logs to %s", filename)
					return
				}
			}(pod.GetName(), container.Name)
		}
	}

	return nil
}
