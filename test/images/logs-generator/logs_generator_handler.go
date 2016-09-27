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
	"fmt"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/docker/docker/pkg/random"
)

const (
	generateMethod = "/generate"

	linesTotalParam = "lines_total"
	durationParam   = "duration"

	parametersBadRequestFormat = "Error parsing parameters: %v"
)

var (
	httpMethods = []string{
		"GET",
		"POST",
		"PUT",
		"DELETE",
	}
	namespaces = []string{
		"kube-system",
		"default",
		"my-custom-namespace",
	}
	resources = []string{
		"pods",
		"services",
		"endpoints",
		"configmaps",
	}
)

type LogsGeneratorHandler struct{}

func NewLogsGeneratorHandler() *LogsGeneratorHandler {
	return &LogsGeneratorHandler{}
}

func (handler *LogsGeneratorHandler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if strings.ToLower(req.URL.Path) == generateMethod {
		linesTotal, durationSeconds, err := parseGenerateParameters(req)

		if err != nil {
			errorMessage := fmt.Sprintf(parametersBadRequestFormat, err)
			http.Error(w, errorMessage, http.StatusBadRequest)
			return
		}

		go handler.generateLogs(linesTotal, durationSeconds)

		return
	}

	http.Error(w, fmt.Sprintf("Unknown method: %s", req.URL.Path), http.StatusNotFound)
}

func (handler *LogsGeneratorHandler) generateLogs(linesTotal int, durationSeconds int) {
	delay := time.Duration(float64(durationSeconds) / float64(linesTotal) * float64(time.Second))
	randomSource := random.NewSource()

	for i := 0; i < linesTotal; i++ {
		fmt.Println(generateLogLine(randomSource, i))

		time.Sleep(delay)
	}
}

func generateLogLine(randomSource rand.Source, id int) string {
	method := httpMethods[int(randomSource.Int63())%len(httpMethods)]
	namespace := namespaces[int(randomSource.Int63())%len(namespaces)]
	resource := resources[int(randomSource.Int63())%len(resources)]
	resourceName := generateRandomName(randomSource)
	url := fmt.Sprintf("/api/v1/namespaces/%s/%s/%s", namespace, resource, resourceName)
	status := 200 + randomSource.Int63()%300

	return fmt.Sprintf("%s %d %s %s %d", time.Now().Format(time.RFC3339), id, method, url, status)
}

func generateRandomName(randomSource rand.Source) string {
	runes := []rune{}
	nameLength := int(8 + randomSource.Int63()%10)
	for i := 0; i < nameLength; i++ {
		nextRune := rune('a' + randomSource.Int63()%26)
		runes = append(runes, nextRune)
	}

	return string(runes)
}

func parseGenerateParameters(req *http.Request) (linesTotal int, durationSeconds int, err error) {
	query := req.URL.Query()

	linesTotalStr := query.Get(linesTotalParam)
	if linesTotalStr == "" {
		err = fmt.Errorf("Missing required parameter %s", linesTotalParam)
		return
	}
	if linesTotal, err = strconv.Atoi(linesTotalStr); err != nil {
		err = fmt.Errorf("Error parsing %s: %v", linesTotalParam, err)
		return
	}

	durationStr := query.Get(durationParam)
	if durationStr == "" {
		err = fmt.Errorf("Missing required parameter %s", durationParam)
		return
	}
	duration, err := time.ParseDuration(durationStr)
	if err != nil {
		err = fmt.Errorf("Error parsing %s: %v", durationParam, err)
		return
	}

	durationSeconds = int(duration.Seconds())
	if durationSeconds <= 0 {
		err = fmt.Errorf("Invalid duration: %v, must be a positive number of seconds", duration)
		return
	}

	return
}
