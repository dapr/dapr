package perf

import "time"

type TestResult struct {
	RunType           string    `json:"RunType"`
	Labels            string    `json:"Labels"`
	StartTime         time.Time `json:"StartTime"`
	RequestedQPS      string    `json:"RequestedQPS"`
	RequestedDuration string    `json:"RequestedDuration"`
	ActualQPS         float64   `json:"ActualQPS"`
	ActualDuration    int64     `json:"ActualDuration"`
	NumThreads        int       `json:"NumThreads"`
	Version           string    `json:"Version"`
	DurationHistogram struct {
		Count  int     `json:"Count"`
		Min    float64 `json:"Min"`
		Max    float64 `json:"Max"`
		Sum    float64 `json:"Sum"`
		Avg    float64 `json:"Avg"`
		StdDev float64 `json:"StdDev"`
		Data   []struct {
			Start   float64 `json:"Start"`
			End     float64 `json:"End"`
			Percent float64 `json:"Percent"`
			Count   int     `json:"Count"`
		} `json:"Data"`
		Percentiles []struct {
			Percentile float64 `json:"Percentile"`
			Value      float64 `json:"Value"`
		} `json:"Percentiles"`
	} `json:"DurationHistogram"`
	Exactly  int `json:"Exactly"`
	RetCodes struct {
		Num200 int `json:"200"`
		Num400 int `json:"400"`
		Num500 int `json:"500"`
	} `json:"RetCodes"`
	Sizes struct {
		Count  int     `json:"Count"`
		Min    int     `json:"Min"`
		Max    int     `json:"Max"`
		Sum    int     `json:"Sum"`
		Avg    float64 `json:"Avg"`
		StdDev float64 `json:"StdDev"`
		Data   []struct {
			Start   int     `json:"Start"`
			End     int     `json:"End"`
			Percent float64 `json:"Percent"`
			Count   int     `json:"Count"`
		} `json:"Data"`
		Percentiles interface{} `json:"Percentiles"`
	} `json:"Sizes"`
	HeaderSizes struct {
		Count  int     `json:"Count"`
		Min    int     `json:"Min"`
		Max    int     `json:"Max"`
		Sum    int     `json:"Sum"`
		Avg    float64 `json:"Avg"`
		StdDev float64 `json:"StdDev"`
		Data   []struct {
			Start   int     `json:"Start"`
			End     int     `json:"End"`
			Percent float64 `json:"Percent"`
			Count   int     `json:"Count"`
		} `json:"Data"`
		Percentiles interface{} `json:"Percentiles"`
	} `json:"HeaderSizes"`
	URL         string `json:"URL"`
	SocketCount int    `json:"SocketCount"`
	AbortOn     int    `json:"AbortOn"`
}
