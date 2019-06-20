package utils

import (
	"os"
	"time"
)

type CrashingTopology struct {
	TestProfiles []TestProfile
}

type TestProfile struct {
	HostProfile HostProfile
}

func NewTestProfile(h HostProfile) *TestProfile {
	p := new(TestProfile)
	p.HostProfile = h
	return p
}

func NewCrashingTopology() *CrashingTopology {
	t := CrashingTopology{}
	t.TestProfiles = make([]TestProfile, 0)
	return &t
}

func (t *CrashingTopology) AddTestHost(p TestProfile) {
	t.TestProfiles = append(t.TestProfiles, p)
}

func (t *CrashingTopology) Run(testCase string, timeOutSecs int) map[string][]string {
	ret := make(map[string][]string)
	hosts := make([]*CrashingHost, 0)
	for _, c := range t.TestProfiles {
		host := NewCrashingHost(c.HostProfile)
		hosts = append(hosts, host)
		go host.Run(os.Args[0], "CRASHING_CASE="+c.HostProfile.Name, "-test.run="+testCase)
	}
	time.Sleep(time.Duration(timeOutSecs) * time.Second)
	for _, h := range hosts {
		h.Stop()
		<-h.Finished
		ret[h.hostProfile.Name] = make([]string, 0)
		for _, o := range h.Outputs {
			ret[h.hostProfile.Name] = append(ret[h.hostProfile.Name], o)
		}
	}
	return ret
}
