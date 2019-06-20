package utils

import (
	"bytes"
	"math/rand"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

type CrashProfile string
type RecoveryProfile string

const (
	FrequentCrash CrashProfile = "frequent"
	RandomCrash   CrashProfile = "random"
	RareCrash     CrashProfile = "rare"
	NeverCrash    CrashProfile = "never"
)
const (
	AlwaysRecover RecoveryProfile = "always"
	NeverRecover  RecoveryProfile = "never"
)

type HostProfile struct {
	CrashProfile    CrashProfile
	RecoveryProfile RecoveryProfile
	Name            string
}
type CrashingHost struct {
	hostProfile HostProfile
	isRunning   bool
	Outputs     []string
	command     *exec.Cmd
	buffer      *bytes.Buffer
	lock        sync.Mutex
	Finished    chan int
}

func NewCrashingHost(profile HostProfile) *CrashingHost {
	h := new(CrashingHost)
	h.hostProfile = profile
	h.Outputs = make([]string, 0)
	h.command = nil
	h.isRunning = true
	h.Finished = make(chan int)
	return h
}

func (h *CrashingHost) Stop() {
	h.isRunning = false
}
func (h *CrashingHost) launch(name string, arg ...string) {
	h.lock.Lock()
	if h.command == nil {
		h.buffer = new(bytes.Buffer)
		h.command = exec.Command(name, arg...)
		h.command.Env = append(os.Environ(), "CRASHING_CASE="+h.hostProfile.Name)
		h.command.Stdout = h.buffer
		h.command.Start()
	}
	h.lock.Unlock()
}
func (h *CrashingHost) crash() {
	h.lock.Lock()
	if h.command != nil {
		h.command.Process.Kill()
		lines := strings.Split(h.buffer.String(), "\n")
		for _, l := range lines {
			if l != "" && l != "PASS" {
				h.Outputs = append(h.Outputs, l)
			}
		}
		h.command = nil
	}
	h.lock.Unlock()
}
func (h *CrashingHost) Run(name string, arg ...string) {
	h.Outputs = make([]string, 0)
	launcherC := make(chan int)
	crasherC := make(chan int)

	go func() {
		for h.isRunning {
			go h.launch(name, arg...)
			time.Sleep(1000 * time.Millisecond)
		}
		launcherC <- 1
	}()

	go func() {
		for h.isRunning {
			r := rand.Intn(10)
			if h.hostProfile.CrashProfile == FrequentCrash && r < 9 ||
				h.hostProfile.CrashProfile == RandomCrash && r >= 5 ||
				h.hostProfile.CrashProfile == RareCrash && r == 7 {
				go h.crash()
			}
			time.Sleep(1000 * time.Millisecond)
		}
		crasherC <- 1
	}()

	_, _ = <-launcherC, <-crasherC

	if h.command != nil {
		h.command.Process.Kill()
		lines := strings.Split(h.buffer.String(), "\n")
		for _, l := range lines {
			if l != "" && l != "PASS" {
				h.Outputs = append(h.Outputs, l)
			}
		}
	}

	h.Finished <- 1
}
