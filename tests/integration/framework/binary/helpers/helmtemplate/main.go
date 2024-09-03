/*
Copyright 2024 The Dapr Authors
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

// some sections (for show files) copied from: https://github.com/helm/helm/blob/2feac15cc3252c97c997be2ced1ab8afe314b429/cmd/helm/template.go
/*
Copyright The Helm Authors.

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
	"bytes"
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"

	"helm.sh/helm/v3/pkg/releaseutil"

	"github.com/spf13/pflag"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/cli"
	"helm.sh/helm/v3/pkg/cli/values"
	"helm.sh/helm/v3/pkg/getter"
)

var settings = cli.New()

func exitWithErr(err error) {
	fmt.Fprintf(os.Stderr, "Error: %s\n", err.Error())
	os.Exit(1)
}

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()

	cfg := new(action.Configuration)
	client := action.NewInstall(cfg)
	client.DryRun = true
	client.ReleaseName = "release-name"
	client.Replace = true
	client.ClientOnly = true
	client.IncludeCRDs = true

	p := getter.All(settings)
	valueOpts := &values.Options{}
	pf := pflag.NewFlagSet("helmtemplate", pflag.ContinueOnError)
	addValueOptionsFlags(pf, valueOpts)
	settings.AddFlags(pf)

	var showFiles []string
	pf.StringArrayVarP(&showFiles, "show-only", "s", []string{}, "only show manifests rendered from the given templates")

	if err := pf.Parse(os.Args[1:]); err != nil {
		exitWithErr(err)
	}

	_, chart, err := client.NameAndChart(pf.Args())
	if err != nil {
		exitWithErr(err)
	}
	cp, err := client.ChartPathOptions.LocateChart(chart, settings)
	if err != nil {
		exitWithErr(err)
	}

	vals, err := valueOpts.MergeValues(p)
	if err != nil {
		exitWithErr(err)
	}
	chartRequested, err := loader.Load(cp)
	if err != nil {
		exitWithErr(err)
	}
	client.Namespace = settings.Namespace()

	rel, err := client.RunWithContext(ctx, chartRequested, vals)
	if err != nil {
		exitWithErr(err)
	}

	if rel == nil {
		return
	}

	var manifests bytes.Buffer
	fmt.Fprintln(&manifests, strings.TrimSpace(rel.Manifest))

	if len(showFiles) == 0 {
		fmt.Fprintf(os.Stdout, "%s", manifests.String())
		return
	}

	// This is necessary to ensure consistent manifest ordering when using --show-only
	// with globs or directory names.
	splitManifests := releaseutil.SplitManifests(manifests.String())
	manifestsKeys := make([]string, 0, len(splitManifests))
	for k := range splitManifests {
		manifestsKeys = append(manifestsKeys, k)
	}
	sort.Sort(releaseutil.BySplitManifestsOrder(manifestsKeys))

	manifestNameRegex := regexp.MustCompile("# Source: [^/]+/(.+)")
	var manifestsToRender []string
	for _, f := range showFiles {
		missing := true
		// Use linux-style filepath separators to unify user's input path
		f = filepath.ToSlash(f)
		for _, manifestKey := range manifestsKeys {
			manifest := splitManifests[manifestKey]
			submatch := manifestNameRegex.FindStringSubmatch(manifest)
			if len(submatch) == 0 {
				continue
			}
			manifestName := submatch[1]
			// manifest.Name is rendered using linux-style filepath separators on Windows as
			// well as macOS/linux.
			manifestPathSplit := strings.Split(manifestName, "/")
			// manifest.Path is connected using linux-style filepath separators on Windows as
			// well as macOS/linux
			manifestPath := strings.Join(manifestPathSplit, "/")

			// if the filepath provided matches a manifest path in the
			// chart, render that manifest
			if matched, _ := filepath.Match(f, manifestPath); !matched {
				continue
			}
			manifestsToRender = append(manifestsToRender, manifest)
			missing = false
		}
		if missing {
			exitWithErr(fmt.Errorf("could not find template %s in chart", f))
		}
	}

	fmt.Fprintf(os.Stdout, "%s", strings.Join(manifestsToRender, "\n---\n"))
}
