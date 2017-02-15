/*
Copyright 2015 The Kubernetes Authors.

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
	"flag"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"fmt"
	"github.com/golang/glog"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"math"
)

var (
	probeInterval = flag.Duration("probe-interval", 100*time.Millisecond, "Files probing interval")
	port          = flag.Int("port", 1234, "Port on which to export metrics")
	channelSize   = flag.Int("chan-size", 100*1000, "Size of operations channel")

	logFilesLocations = []string{
		"/var/log",
		"/var/log/containers",
	}
	positionFilesLocation = "/var/log"

	// Number of bytes in the log file
	logFileActualBytes = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "log_file_actual_bytes",
		Help: "Actual size of log files",
	}, []string{"log_name"})

	// Number of bytes ingested by fluentd
	logFileIngestedBytes = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "log_file_ingested_bytes",
		Help: "Number of bytes, ingested to fluentd",
	}, []string{"log_name"})

	// Number of bytes ingested by fluentd
	logFileLostBytes = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "log_file_lost_bytes",
		Help: "Number of bytes, lost by fluentd",
	}, []string{"log_name"})
)

const (
	logFileExtension = ".log"
	posFileExtension = ".pos"

	logFileWasChanged LogFileOperationType = iota
	posFileWasChanged LogFileOperationType = iota
)

type LogFileOperationType int

type LogFileOperation struct {
	FileName      string
	FileSize      int64
	OperationType LogFileOperationType
}

func init() {
	prometheus.MustRegister(logFileActualBytes)
	prometheus.MustRegister(logFileIngestedBytes)
}

// TODO: GC metrics

func main() {
	operations := make(chan LogFileOperation, *channelSize)
	go readLogFiles(*probeInterval, operations, logFilesLocations)
	go readIngestedSizes(*probeInterval, operations, positionFilesLocation)
	go processOperations(operations)

	http.Handle("/metrics", prometheus.Handler())
	if err := http.ListenAndServe(fmt.Sprintf(":%d", *port), nil); err != nil {
		glog.Fatal(err)
	}
}

func readLogFiles(probeInterval time.Duration, operations chan LogFileOperation, baseLocations []string) {
	for range time.Tick(probeInterval) {
		probeFiles(baseLocations, func(absPath string) {
			if filepath.Ext(absPath) != logFileExtension {
				return
			}

			stat, err := os.Stat(absPath)
			if err != nil {
				glog.Warningf("Failed to stat file %s", absPath)
				return
			}

			operations <- LogFileOperation{
				FileName:      absPath,
				FileSize:      stat.Size(),
				OperationType: logFileWasChanged,
			}
		})
	}
}

func readIngestedSizes(probeInterval time.Duration, operations chan LogFileOperation, baseLocation string) {
	for range time.Tick(probeInterval) {
		probeFiles([]string{baseLocation}, func(absPath string) {
			if filepath.Ext(absPath) != posFileExtension {
				return
			}

			contents, err := ioutil.ReadFile(absPath)
			if err != nil {
				glog.Warningf("Failed to read file %s", absPath)
				return
			}
			lines := strings.Split(string(contents), "\n")

			for _, posLine := range lines {
				if fileName, fileSize, ok := tryParsePosLine(posLine); ok {
					operations <- LogFileOperation{
						FileName:      fileName,
						FileSize:      fileSize,
						OperationType: posFileWasChanged,
					}
				}
			}
		})
	}
}

func processOperations(operations chan LogFileOperation) {
	for operation := range operations {
		prev_actual_size, has_prev_actual_size := extractLastGaugeValue(logFileActualBytes, operation.FileName)
		prev_ingested_size, has_prev_ingested_size := extractLastGaugeValue(logFileIngestedBytes, operation.FileName)

		if operation.OperationType == posFileWasChanged {
			logFileIngestedBytes.WithLabelValues(operation.FileName).Set(float64(operation.FileSize))
		}

		if operation.OperationType == logFileWasChanged {
			logFileActualBytes.WithLabelValues(operation.FileName).Set(float64(operation.FileSize))

			if has_prev_actual_size && has_prev_ingested_size && prev_actual_size < float64(operation.FileSize) {
				delta := math.Max(0.0, float64(prev_actual_size-prev_ingested_size))
				logFileLostBytes.WithLabelValues(operation.FileName).Add(delta)
			}
		}
	}
}
func extractLastGaugeValue(gaugeVec *prometheus.GaugeVec, fields ...string) (float64, bool) {
	var metric_pb dto.Metric
	if err := gaugeVec.WithLabelValues(fields...).Write(&metric_pb); err != nil {
		return 0, false
	}

	if metric_pb.Gauge.Value == nil {
		return 0, false
	}

	return *metric_pb.Gauge.Value, true
}

func probeFiles(locations []string, callback func(string)) {
	for _, location := range locations {
		fileInfos, err := ioutil.ReadDir(location)
		if err != nil {
			glog.Warningf("failed to list files in '%s'", location)
			continue
		}

		for _, fileInfo := range fileInfos {
			absPath := filepath.Join(location, fileInfo.Name())
			callback(absPath)
		}
	}
}

func tryParsePosLine(posLine string) (string, int64, bool) {
	chunks := strings.Split(posLine, "\t")
	if len(chunks) < 3 {
		return "", 0, false
	}

	fileName := chunks[0]
	fileSize, err := strconv.ParseInt(chunks[1], 16, 64)
	if err != nil {
		return "", 0, false
	}

	return fileName, fileSize, true
}
