package main

import (
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
)

func main() {
	var files []string

	root := "./apps"
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			panic(err)
		}
		if info.IsDir() {
			_, err := os.Stat(path + "/go.mod")
			if !os.IsNotExist(err) {
				files = append(files, path)
			}
		}
		return nil
	})

	testsPath, _ := os.Getwd()
	goExecutable, _ := exec.LookPath("go")
	for _, file := range files {
		os.Chdir(file)
		_, err := RunCmdAndWait(goExecutable, "get", "-u", "github.com/dapr/dapr@master")
		if err != nil {
			panic(err)
		}
		fmt.Println("Successfully modified dapr dependencies for: ", file)
		// Go back to the apps dir
		os.Chdir(testsPath)
	}
}

// RunCmdAndWait runs and waits for a shell command to complete
func RunCmdAndWait(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "", err
	}

	err = cmd.Start()
	if err != nil {
		return "", err
	}

	resp, err := ioutil.ReadAll(stdout)
	if err != nil {
		return "", err
	}
	errB, err := ioutil.ReadAll(stderr)
	if err != nil {
		return "", nil
	}

	err = cmd.Wait()
	if err != nil {
		// in case of error, capture the exact message
		if len(errB) > 0 {
			return "", errors.New(string(errB))
		}
		return "", err
	}

	return string(resp), nil
}
