package cmd

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"

	"github.com/spf13/cobra"
	"golang.org/x/mod/semver"
	"gopkg.in/yaml.v3"
)

var (
	ErrVersionNotSupported = errors.New("version not supported")
	ErrVersionNotFound     = errors.New("version not found")
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

func compareVersions(path string) (string, error) {
	workflowVersion, err := parseWorkflowVersionFromFile(path)
	if err != nil {
		return fmt.Sprintf("Error parsing workflow version: %v", err), ErrVersionNotFound
	}
	currentVersion, err := getCurrentVersion()
	if err != nil {
		return fmt.Sprintf("Error getting current version: %v", err), ErrVersionNotFound
	}
	validVersion := isVersionValid(workflowVersion, currentVersion)
	if !validVersion {
		return fmt.Sprintf("Invalid version, expected: %s, current: %s ", workflowVersion, currentVersion), ErrVersionNotSupported
	}
	return fmt.Sprintf("Linter version is valid (MajorMinor): %s", currentVersion), nil
}

func getCmdCheckLint(cmdType string) *cobra.Command {
	// Base command
	cmd := &cobra.Command{
		Use:   cmdType,
		Short: "Compare local golangci-lint version against workflow version",
		Run: func(cmd *cobra.Command, args []string) {
			path := cmd.Flag("path").Value.String()
			res, err := compareVersions(path)
			fmt.Println(res)
			if err != nil {
				fmt.Println("Please install the correct version using the guide - https://golangci-lint.run/usage/install/")
				if err == ErrVersionNotSupported {
					fmt.Println("Alternatively review the golangci-lint version in the workflow file at .github/workflows/dapr.yml")
				}
				os.Exit(1)
			}
		},
	}
	cmd.PersistentFlags().String("path", "../.github/workflows/dapr.yml", "Path to workflow file")
	return cmd
}

func init() {
	// checkLintCmd represents the checkLint command
	checkLintCmd := getCmdCheckLint("check-linter")
	rootCmd.AddCommand(checkLintCmd)
}
