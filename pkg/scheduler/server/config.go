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

package server

import (
	"log"
	"net/url"

	"go.etcd.io/etcd/server/v3/embed"
)

func parseEtcdUrls(strs []string) []url.URL {
	urls := make([]url.URL, 0, len(strs))
	for _, str := range strs {
		u, err := url.Parse(str)
		if err != nil {
			log.Printf("Invalid url %s, error: %s", str, err.Error())
			continue
		}
		urls = append(urls, *u)
	}
	return urls

}

func conf() *embed.Config {
	config := embed.NewConfig()
	config.Name = "localhost"
	config.Dir = "/tmp/my-embedded-ectd-cluster"
	// config.LPUrls = parseEtcdUrls([]string{"http://0.0.0.0:2380"})
	// config.LCUrls = parseEtcdUrls([]string{"http://0.0.0.0:2379"})
	// config.APUrls = parseEtcdUrls([]string{"http://localhost:2380"})
	// config.ACUrls = parseEtcdUrls([]string{"http://localhost:2379"})
	config.InitialCluster = "localhost=http://localhost:2380"
	return config
}
