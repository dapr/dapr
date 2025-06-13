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

package cmd

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/google/go-containerregistry/pkg/crane"
	gitignore "github.com/sabhiram/go-gitignore"
	"github.com/spf13/cobra"
)

// Manages the commands for e2e and perf
type cmdE2EPerf struct {
	cmdType string
	flags   *cmdE2EPerfFlags
}

// Flags for the e2e/perf commands
type cmdE2EPerfFlags struct {
	AppDir           string
	DestRegistry     string
	DestTag          string
	CacheRegistry    string
	Dockerfile       string
	TargetOS         string
	TargetArch       string
	IgnoreFile       string
	CacheIncludeFile string
	Name             string
	WindowsVersion   string
}

// Returns the command for e2e or perf
func getCmdE2EPerf(cmdType string) *cobra.Command {
	// Object
	obj := &cmdE2EPerf{
		cmdType: cmdType,
		flags:   &cmdE2EPerfFlags{},
	}

	// Base command
	cmd := &cobra.Command{
		Use:   cmdType,
		Short: fmt.Sprintf("Build and push %s test apps", cmdType),
		Long:  fmt.Sprintf(`Tools to build %s test apps, including building and pushing Docker containers.`, cmdType),
	}

	// "build" sub-command
	buildCmd := &cobra.Command{
		Use:   "build",
		Short: "Build Docker image locally",
		Long: fmt.Sprintf(`Build a %s test app and its Docker images.
If the image is available in the cache and is up-to-date, it will be pulled from the cache instead.
If the "--cache-registry" option is set, it will be pushed to the cache too.
`, cmdType),
		RunE: obj.buildCmd,
	}
	buildCmd.Flags().StringVarP(&obj.flags.Name, "name", "n", "", "Name of the app")
	buildCmd.MarkFlagRequired("name")
	buildCmd.Flags().StringVarP(&obj.flags.AppDir, "appdir", "d", "", "Directory where the test apps are stored")
	buildCmd.MarkFlagRequired("appdir")
	buildCmd.MarkFlagDirname("appdir")
	buildCmd.Flags().StringVar(&obj.flags.DestRegistry, "dest-registry", "", "Registry for the Docker image")
	buildCmd.MarkFlagRequired("dest-registry")
	buildCmd.Flags().StringVar(&obj.flags.DestTag, "dest-tag", "", "Tag to apply to the Docker image")
	buildCmd.MarkFlagRequired("dest-tag")
	buildCmd.Flags().StringVar(&obj.flags.CacheRegistry, "cache-registry", "", "Cache container registry (optional)")
	buildCmd.Flags().StringVar(&obj.flags.Dockerfile, "dockerfile", "Dockerfile", "Dockerfile to use")
	buildCmd.Flags().StringVar(&obj.flags.TargetOS, "target-os", runtime.GOOS, "Target OS")
	buildCmd.Flags().StringVar(&obj.flags.TargetArch, "target-arch", runtime.GOARCH, "Target architecture")
	buildCmd.Flags().StringVar(&obj.flags.IgnoreFile, "ignore-file", ".gitignore", "Name of the file with files to exclude (in the format of .gitignore)")
	buildCmd.Flags().StringVar(&obj.flags.CacheIncludeFile, "cache-include-file", ".cache-include", "Name of the file inside the app folder with additional files to include in checksumming (in the format of .gitignore)")
	buildCmd.Flags().StringVar(&obj.flags.WindowsVersion, "windows-version", "", "Windows version to use for Windows containers")

	// "push" sub-command
	pushCmd := &cobra.Command{
		Use:   "push",
		Short: "push Docker image",
		Long: fmt.Sprintf(`Pushes the pre-built Docker image for a %s test app.
The image must have been aleady built using the "build" sub-command.
`, cmdType),
		RunE: obj.pushCmd,
	}
	pushCmd.Flags().StringVarP(&obj.flags.Name, "name", "n", "", "Name of the app")
	pushCmd.MarkFlagRequired("name")
	pushCmd.Flags().StringVar(&obj.flags.DestRegistry, "dest-registry", "", "Registry for the Docker image")
	pushCmd.MarkFlagRequired("dest-registry")
	pushCmd.Flags().StringVar(&obj.flags.DestTag, "dest-tag", "", "Tag to apply to the Docker image")
	pushCmd.MarkFlagRequired("dest-tag")

	// "build-and-push" sub-command
	buildAndPushCmd := &cobra.Command{
		Use:   "build-and-push",
		Short: "Build and push Docker image",
		Long: fmt.Sprintf(`Single command to build and push the Docker image for a %s test app.
If the "--cahce-registry" option is set and the image exists in the cache, it will be copied directly from the cache, without pulling it locally first.
`, cmdType),
		RunE: obj.buildAndPushCmd,
	}
	buildAndPushCmd.Flags().StringVarP(&obj.flags.Name, "name", "n", "", "Name of the app")
	buildAndPushCmd.MarkFlagRequired("name")
	buildAndPushCmd.Flags().StringVarP(&obj.flags.AppDir, "appdir", "d", "", "Directory where the test apps are stored")
	buildAndPushCmd.MarkFlagRequired("appdir")
	buildAndPushCmd.MarkFlagDirname("appdir")
	buildAndPushCmd.Flags().StringVar(&obj.flags.DestRegistry, "dest-registry", "", "Registry for the Docker image")
	buildAndPushCmd.MarkFlagRequired("dest-registry")
	buildAndPushCmd.Flags().StringVar(&obj.flags.DestTag, "dest-tag", "", "Tag to apply to the Docker image")
	buildAndPushCmd.MarkFlagRequired("dest-tag")
	buildAndPushCmd.Flags().StringVar(&obj.flags.CacheRegistry, "cache-registry", "", "Cache container registry (optional)")
	buildAndPushCmd.Flags().StringVar(&obj.flags.Dockerfile, "dockerfile", "Dockerfile", "Dockerfile to use")
	buildAndPushCmd.Flags().StringVar(&obj.flags.TargetOS, "target-os", runtime.GOOS, "Target OS")
	buildAndPushCmd.Flags().StringVar(&obj.flags.TargetArch, "target-arch", runtime.GOARCH, "Target architecture")
	buildAndPushCmd.Flags().StringVar(&obj.flags.IgnoreFile, "ignore-file", ".gitignore", "Name of the file with files to exclude (in the format of .gitignore)")
	buildAndPushCmd.Flags().StringVar(&obj.flags.CacheIncludeFile, "cache-include-file", ".cache-include", "Name of the file inside the app folder with additional files to include in checksumming (in the format of .gitignore)")
	buildAndPushCmd.Flags().StringVar(&obj.flags.WindowsVersion, "windows-version", "", "Windows version to use for Windows containers")

	// Register the commands
	cmd.AddCommand(buildCmd)
	cmd.AddCommand(pushCmd)
	cmd.AddCommand(buildAndPushCmd)

	return cmd
}

// Handler for the "build" sub-command
func (c *cmdE2EPerf) buildCmd(cmd *cobra.Command, args []string) error {
	// Target image and tag
	destImage := c.getDestImage()

	// Compute the hash of the folder with the app, then returns the full Docker image name (including the tag, which is based on the hash)
	cachedImage, err := c.getCachedImage()
	if err != nil {
		return err
	}

	// If cache is enabled, try pulling from cache first
	if c.flags.CacheRegistry != "" {
		fmt.Printf("Looking for image %s in cache…\n", cachedImage)

		// If there's no error, the image was pulled from the cache
		// Just tag the image and we're done
		if exec.Command("docker", "pull", cachedImage).Run() == nil {
			fmt.Printf("Image pulled from the cache: %s\n", cachedImage)
			err = exec.Command("docker", "tag", cachedImage, destImage).Run()

			// Return, whether we have an erorr or not, as we're done
			return err
		} else {
			fmt.Printf("Image not found in cache; it will be built: %s\n", cachedImage)
		}
	}

	// Build the image
	// It also pushes to the cache registry if needed
	err = c.buildDockerImage(cachedImage)
	if err != nil {
		return err
	}
	return nil
}

// Handler for the "push" sub-command
func (c *cmdE2EPerf) pushCmd(cmd *cobra.Command, args []string) error {
	// Target image and tag
	destImage := c.getDestImage()

	// Push the image
	err := c.pushDockerImage(destImage)
	if err != nil {
		return err
	}

	return nil
}

// Handler for the "build-and-push" sub-command
func (c *cmdE2EPerf) buildAndPushCmd(cmd *cobra.Command, args []string) error {
	// Target image and tag
	destImage := c.getDestImage()

	// Cached image and tag
	cachedImage, err := c.getCachedImage()
	if err != nil {
		return err
	}

	// Try to copy the image between the cache and the target directly, without pulling first
	// This will fail if the image is not in cache, and that's ok
	if c.flags.CacheRegistry != "" {
		fmt.Printf("Trying to copy image %s directly from cache %s\n", destImage, cachedImage)
		err = crane.Copy(cachedImage, destImage)
		if err == nil {
			// If there's no error, we're done
			fmt.Println("Image copied from cache directly! You're all set.")
			return nil
		}

		// Copying the image failed, so we'll resort to build + push
		fmt.Printf("Copying image directly from cache failed with error '%s'. Will build image.\n", err)
	} else {
		fmt.Println("Cache registry not set: will not use cache")
	}

	// Build the image
	// It also pushes to the cache registry if needed
	err = c.buildDockerImage(cachedImage)
	if err != nil {
		return err
	}

	// Push the image
	err = c.pushDockerImage(destImage)
	if err != nil {
		return err
	}

	return nil
}

// Returns the cached image name and tag
func (c *cmdE2EPerf) getCachedImage() (string, error) {
	// Get the hash of the files in the directory
	hashDir, err := c.getHashDir()
	if err != nil {
		return "", err
	}

	tag := fmt.Sprintf("%s-%s-%s", c.flags.TargetOS, c.flags.TargetArch, hashDir)

	if c.flags.WindowsVersion != "" {
		tag = fmt.Sprintf("%s-%s-%s-%s", c.flags.TargetOS, c.flags.WindowsVersion, c.flags.TargetArch, hashDir)
	}

	cachedImage := fmt.Sprintf("%s/%s-%s:%s", c.flags.CacheRegistry, c.cmdType, c.flags.Name, tag)
	return cachedImage, nil
}

// Returns the target image name and tag
func (c *cmdE2EPerf) getDestImage() string {
	return fmt.Sprintf("%s/%s-%s:%s", c.flags.DestRegistry, c.cmdType, c.flags.Name, c.flags.DestTag)
}

// Returns the directory where the app is stored
func (c *cmdE2EPerf) getAppDir() string {
	if c.cmdType == "perf" {
		return filepath.Join(c.flags.AppDir, "perf")
	} else {
		return c.flags.AppDir
	}
}

// Builds a Docker image for the app
// It also pushes it to the cache registry if that's enabled
func (c *cmdE2EPerf) buildDockerImage(cachedImage string) error {
	destImage := c.getDestImage()
	appDir := c.getAppDir()

	// First, check if the image has its own Dockerfile
	dockerfile := filepath.Join(appDir, c.flags.Name, c.flags.Dockerfile)
	_, err := os.Stat(dockerfile)
	if err != nil {
		// App doesn't have a Dockerfile
		// First, compile the Go app
		ext := ""
		if c.flags.TargetOS == "windows" {
			ext = ".exe"
		}

		e := exec.Command("go",
			"build",
			"-o", "app"+ext,
			".",
		)
		e.Env = os.Environ()
		e.Env = append(
			e.Env,
			"CGO_ENABLED=0",
			"GOOS="+c.flags.TargetOS,
			"GOARCH="+c.flags.TargetArch,
		)
		e.Dir = filepath.Join(appDir, c.flags.Name)
		e.Stdout = os.Stdout
		e.Stderr = os.Stderr
		err = e.Run()
		if err != nil {
			fmt.Println("'go build' returned an error:", err)
			return err
		}

		// Use the "shared" Dockerfile
		if c.cmdType == "perf" {
			dockerfile = filepath.Join(appDir, "..", "Dockerfile")
		} else {
			dockerfile = filepath.Join(appDir, c.flags.Dockerfile)
		}
	}

	// Build the Docker image
	fmt.Printf("Building Docker image: %s\n", destImage)
	args := []string{
		"build",
		"-f", dockerfile,
		"-t", destImage,
		filepath.Join(appDir, c.flags.Name, "."),
	}
	switch c.flags.TargetArch {
	case "arm64":
		args = append(args, "--platform", c.flags.TargetOS+"/arm64/v8")
	case "amd64":
		args = append(args, "--platform", c.flags.TargetOS+"/amd64")
	default:
		args = append(args, "--platform", c.flags.TargetOS+"/amd64")
	}
	if c.flags.WindowsVersion != "" {
		args = append(args, "--build-arg", "WINDOWS_VERSION="+c.flags.WindowsVersion)
	}

	fmt.Printf("Running 'docker %s'\n", strings.Join(args, " "))
	e := exec.Command("docker", args...)

	e.Stdout = os.Stdout
	e.Stderr = os.Stderr
	err = e.Run()
	if err != nil {
		fmt.Println("'docker build' returned an error:", err)
		return err
	}

	// Push to the cache, if needed
	if c.flags.CacheRegistry != "" {
		fmt.Printf("Pushing image %s to cache…\n", cachedImage)

		err = exec.Command("docker", "tag", destImage, cachedImage).Run()
		if err != nil {
			fmt.Println("'docker tag' returned an error:", err)
			return err
		}

		e := exec.Command("docker", "push", cachedImage)
		e.Stdout = os.Stdout
		e.Stderr = os.Stderr
		err = e.Run()
		// If there's an error, we probably didn't have permissions to push to the registry, so we can just ignore that
		if err != nil {
			fmt.Println("Failed to push to the cache registry; ignored")
		}
	}

	return nil
}

// Pushes the pre-built Docker image to the target registry
func (c *cmdE2EPerf) pushDockerImage(destImage string) error {
	fmt.Println("Pushing image", destImage)
	e := exec.Command("docker", "push", destImage)
	e.Stdout = os.Stdout
	e.Stderr = os.Stderr
	err := e.Run()
	if err != nil {
		fmt.Println("'docker push' returned an error:", err)
		return err
	}

	return nil
}

// Loads the ".gitignore" (or whatever the value of ignoreFile is) in the appDir and in the appDir/name folders
func (c *cmdE2EPerf) getIgnores() *gitignore.GitIgnore {
	appDir := c.getAppDir()
	files := []string{
		filepath.Join(appDir, c.flags.IgnoreFile),
		// Add the ".gitignore" inside the app's folder too if it exists
		filepath.Join(appDir, c.flags.Name, c.flags.IgnoreFile),
	}
	lines := []string{}
	for _, f := range files {
		read, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		lines = append(lines, strings.Split(string(read), "\n")...)
	}

	if len(lines) == 0 {
		return nil
	}

	return gitignore.CompileIgnoreLines(lines...)
}

// Loads the ".cache-include" (or whatever the value of "includeFile" is) in the appDir/name folder
func (c *cmdE2EPerf) getIncludes() []string {
	appDir := c.getAppDir()
	read, err := os.ReadFile(
		filepath.Join(appDir, c.flags.Name, c.flags.CacheIncludeFile),
	)
	if err != nil || len(read) == 0 {
		// Just ignore errors
		return nil
	}

	return strings.Split(string(read), "\n")
}

func hashFilesInDir(basePath string, ignores *gitignore.GitIgnore) ([]string, error) {
	files := []string{}

	err := filepath.WalkDir(basePath, func(path string, d fs.DirEntry, _ error) error {
		// Check if the file is ignored
		relPath, err := filepath.Rel(basePath, path)
		if err != nil {
			return err
		}

		// Skip the folders and ignored files
		if relPath == "." || d.IsDir() || (ignores != nil && ignores.MatchesPath(path)) {
			return nil
		}

		// Add the hash of the file
		checksum, err := hashEntryForFile(path, relPath)
		if err != nil {
			return err
		}

		files = append(files, checksum)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}

func hashEntryForFile(path string, relPath string) (string, error) {
	// Compute the sha256 of the file
	checksum, err := checksumFile(path)
	if err != nil {
		return "", err
	}

	// Convert all slashes to / so the hash is the same on Windows and Linux
	relPath = filepath.ToSlash(relPath)

	return relPath + " " + checksum, nil
}

// Returns the checksum of the files in the directory
func (c *cmdE2EPerf) getHashDir() (string, error) {
	basePath := filepath.Join(c.getAppDir(), c.flags.Name)
	_, err := os.Stat(basePath)
	if err != nil {
		fmt.Printf("could not find app %s\n", basePath)
		return "", err
	}

	// Load the files to exclude
	ignores := c.getIgnores()

	// Load additional paths to include
	includes := c.getIncludes()

	// Compute the hash of the app's files
	files, err := hashFilesInDir(basePath, ignores)
	if err != nil {
		return "", err
	}
	if len(files) == 0 {
		return "", fmt.Errorf("no file found in the folder")
	}

	// Include other files or folders as needed
	for _, pattern := range includes {
		if !filepath.IsAbs(pattern) {
			pattern = filepath.Join(basePath, pattern)
		}
		matches, err := filepath.Glob(pattern)
		if err != nil || len(matches) == 0 {
			continue
		}
		for _, match := range matches {
			if match == "" {
				continue
			}
			info, err := os.Stat(match)
			if err != nil {
				continue
			}
			if info.IsDir() {
				// Note: we are not passing "ignores" here because .gitignore files are usually specific for the folder they live in, while "match" is often outside of the app's folder.
				// Best is to make sure that include paths are specific, such as ending with `*.go` or `*.proto`, rather than including the entire folder.
				addFiles, err := hashFilesInDir(match, nil)
				if err != nil || len(addFiles) == 0 {
					continue
				}
				files = append(files, addFiles...)
			} else {
				relPath, err := filepath.Rel(basePath, match)
				if err != nil {
					continue
				}
				checksum, err := hashEntryForFile(match, relPath)
				if err != nil {
					continue
				}
				files = append(files, checksum)
			}
		}
	}

	// Sort files to have a consistent order, then compute the checksum of that slice (getting the first 10 chars only)
	sort.Strings(files)
	fileList := strings.Join(files, "\n")
	hashDir := checksumString(fileList)[0:10]

	return hashDir, nil
}

// Calculates the checksum of a file
func checksumFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		fmt.Printf("failed to open file %s for hashing: %v\n", path, err)
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	_, err = io.Copy(h, f)
	if err != nil {
		fmt.Printf("failed to copy file %s into hasher: %v\n", path, err)
		return "", err
	}

	res := hex.EncodeToString(h.Sum(nil))
	return res, nil
}

// Calculates the checksum of a string
func checksumString(str string) string {
	h := sha256.New()
	h.Write([]byte(str))
	return hex.EncodeToString(h.Sum(nil))
}
