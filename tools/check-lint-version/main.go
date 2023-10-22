package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"regexp"

	"golang.org/x/mod/semver"
	"gopkg.in/yaml.v3"
)

type GHWorkflow struct {
	Jobs struct {
		Lint struct {
			Env struct {
				GOVER           string `yaml:"GOVER"`
				GOLANGCILINTVER string `yaml:"GOLANGCILINT_VER"`
			} `yaml:"env"`
		} `yaml:"lint"`
	} `yaml:"jobs"`
}

func parseWorkflowVersionFromFile(path string) (string, error) {
	var ghWorkflow GHWorkflow

	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	err = yaml.Unmarshal(raw, &ghWorkflow)
	if err != nil {
		return "", err
	}
	return ghWorkflow.Jobs.Lint.Env.GOLANGCILINTVER, err
}

func getCurrentVersion() (string, error) {
	out, err := exec.Command("golangci-lint", "--version").Output()
	if err != nil {
		return "", err
	}

	regex, err := regexp.Compile(`golangci-lint\shas\sversion\sv?([\d+.]+[\d])`)
	if err != nil {
		return "", err
	}

	matches := regex.FindStringSubmatch(string(out))

	if matches == nil {
		return "", fmt.Errorf("no version found: %v", string(out))
	}
	return fmt.Sprintf("v%s", matches[1]), err
}

func isVersionValid(workflowVersion, currentVersion string) bool {
	res := semver.MajorMinor(workflowVersion) == semver.MajorMinor(currentVersion)
	return res
}

func compareVersions(path string) string {
	workflowVersion, err := parseWorkflowVersionFromFile(path)
	if err != nil {
		return fmt.Sprintf("Error parsing workflow version: %v", err)
	}
	currentVersion, err := getCurrentVersion()
	if err != nil {
		return fmt.Sprintf("Error getting current version: %v", err)
	}
	validVersion := isVersionValid(workflowVersion, currentVersion)
	if !validVersion {
		return fmt.Sprintf("Invalid version, expected: %s, current: %s - See: https://golangci-lint.run/usage/install/ for instructions to update", workflowVersion, currentVersion)
	}
	return fmt.Sprintf("Linter version is valid (MajorMinor): %s", currentVersion)
}

func main() {
	log.Println(compareVersions("../../.github/workflows/dapr.yml"))
}
