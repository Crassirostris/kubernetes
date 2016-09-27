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

package e2e

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"k8s.io/kubernetes/pkg/api"
	"k8s.io/kubernetes/pkg/util/intstr"
	"k8s.io/kubernetes/test/e2e/framework"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = framework.KubeDescribe("Cluster level logging stress-test using GCL", func() {
	f := framework.NewDefaultFramework("gcl-logging-stress")

	BeforeEach(func() {
		framework.SkipUnlessProviderIs("gce")
	})

	It("should check that logs from pods on all nodes are ingested into GCL", func() {
		ClusterLevelLoggingStressWithGcl(f)
	})
})

const (
	logGenerationDuration = 1 * time.Minute
	linesPerPod           = 200 * 60

	podsPerNode = 10

	logGeneratorContainerName = "log-generator"

	logGeneratorPort = 8080
)

// ClusterLevelLoggingWithGcl is an end to end test for cluster level logging.
func ClusterLevelLoggingStressWithGcl(f *framework.Framework) {
	// Wait for the Fluentd pods to enter the running state.
	By("Checking to make sure the Fluentd pod are running on each healthy node")
	// Obtain a list of healthy nodes so we can place one synthetic logger on each node.
	nodes := getHealthyNodes(f)
	fluentdPods, err := getFluentdPods(f)
	Expect(err).NotTo(HaveOccurred(), "Failed to obtain fluentd pods")
	err = waitForFluentdPods(f, nodes, fluentdPods)
	Expect(err).NotTo(HaveOccurred(), "Failed to wait for fluentd pods entering running state")

	By("Creating log generators")
	podNames, serviceNames, err := createLogGenerators(f, nodes)
	Expect(err).NotTo(HaveOccurred(), "Failed to create log generators")

	By("Waiting for log generators to start")
	err = waitForLogGenerators(f, podNames)
	Expect(err).NotTo(HaveOccurred(), "Failed to wait for log generators to start")

	By("Sending requests to log generators")
	err = sendLoggingRequests(f, serviceNames)
	Expect(err).NotTo(HaveOccurred(), "Failed to send requests to log generators")

	By("Waiting for log generators to finish")
	time.Sleep(logGenerationDuration)

	// Make several attempts to observe the logs ingested into GCL
	By("Checking all the log lines were ingested into GCL")
	totalMissing, missingPerNode := waitForStressLogsToIngest(podNames)

	for podName, missing := range missingPerNode {
		if len(missing) == 0 {
			continue
		}

		missingString := createMissingString(missing)
		framework.Logf("Pod %d is missing %d lines of logs: %s", podName, len(missing), missingString)
	}

	if totalMissing != 0 {
		framework.Failf("Failed to find all %d log lines", len(nodes.Items)*countTo)
	}
}

func createLogGenerators(f *framework.Framework, nodes *api.NodeList) (podNames []string, serviceNames []string, err error) {
	namespace := f.Namespace.Name
	podBaseName := namespace + "-log-generator-pod"
	serviceBaseName := namespace + "-log-generator-service"

	for nodeIdx, node := range nodes.Items {
		for podIdx := 0; podIdx < podsPerNode; podIdx++ {
			podName := fmt.Sprintf("%s-%d-%d", podBaseName, nodeIdx, podIdx)
			podNames = append(podNames, podName)
			portName := podName + "-port"

			if _, err = f.Client.Pods(namespace).Create(&api.Pod{
				ObjectMeta: api.ObjectMeta{
					Name: podName,
					Labels: map[string]string{
						"app": podName,
					},
				},
				Spec: api.PodSpec{
					Containers: []api.Container{
						{
							Name:  logGeneratorContainerName,
							Image: "gcr.io/google_containers/log-generator",
							Ports: []api.ContainerPort{
								{
									Name:          portName,
									ContainerPort: logGeneratorPort,
									Protocol:      api.ProtocolTCP,
								},
							},
						},
					},
					NodeName:      node.Name,
					RestartPolicy: api.RestartPolicyNever,
				},
			}); err != nil {
				return
			}

			serviceName := fmt.Sprintf("%s-%d-%d", serviceBaseName, nodeIdx, podIdx)
			serviceNames = append(serviceNames, serviceName)

			if _, err = f.Client.Services(namespace).Create(&api.Service{
				ObjectMeta: api.ObjectMeta{
					Name: serviceName,
				},
				Spec: api.ServiceSpec{
					Ports: []api.ServicePort{
						{
							Name:       portName,
							TargetPort: intstr.IntOrString{IntVal: 80},
							Protocol:   api.ProtocolTCP,
						},
					},
					Selector: map[string]string{
						"app": podName,
					},
				},
			}); err != nil {
				return
			}
		}
	}

	return
}

func waitForLogGenerators(f *framework.Framework, podNames []string) error {
	for _, podName := range podNames {
		pod, err := f.Client.Pods(f.Namespace.Name).Get(podName)
		if err != nil {
			return err
		}

		if err := framework.WaitForPodRunningInNamespace(f.Client, pod); err != nil {
			return err
		}
	}

	return nil
}

func sendLoggingRequests(f *framework.Framework, serviceNames []string) error {
	for _, serviceName := range serviceNames {
		proxyRequest, err := framework.GetServicesProxyRequest(f.Client, f.Client.Get())
		if err != nil {
			return err
		}

		result := proxyRequest.Namespace(f.Namespace.Name).
			Name(serviceName).
			Suffix("generate").
			Param("lines_total", strconv.Itoa(linesPerPod)).
			Param("duration", logGenerationDuration.String()).
			Do()

		if result.Error() != nil {
			return result.Error()
		}

		var statusCode int
		result.StatusCode(&statusCode)
		if statusCode != http.StatusOK {
			return fmt.Errorf("Unexpected status code: %d", statusCode)
		}
	}

	return nil
}

func createMissingString(missingEntries []int) (result string) {
	for i := range missingEntries {
		if i != 0 {
			result += ", "
		}
		result += strconv.Itoa(missingEntries[i])
	}

	return
}

func waitForStressLogsToIngest(podNames []string) (totalMissing int, missingPerPod map[string][]int) {
	for _, podName := range podNames {
		missing := make([]int, linesPerPod)
		for i := 0; i < linesPerPod; i++ {
			missing[i] = i
		}
		missingPerPod[podName] = missing
	}

	for start := time.Now(); time.Since(start) < ingestionTimeout; time.Sleep(25 * time.Second) {
		newMissingPerPod := make(map[string][]int)
		for _, podName := range podNames {
			filter := fmt.Sprintf("resource.labels.pod_id=%s", podName)
			newMissingPerPod[podName] = missingPerPod[podName]

			entries, err := readFilteredEntriesFromGcl(filter)
			if err != nil {
				framework.Logf("Failed to read events from gcl after %v due to %v", time.Since(start), err)
				continue
			}

			newMissingPerPod[podName] = analyzeEntries(entries)
		}

		missingPerPod := newMissingPerPod
		totalMissing = 0
		for _, missing := range missingPerPod {
			totalMissing += len(missing)
		}

		if totalMissing == 0 {
			break
		}

		framework.Logf("Still missing %d lines", totalMissing)
		continue
	}

	return
}

func analyzeEntries(entries []*LogEntry) (missing []int) {
	count := make(map[int]int)

	for _, entry := range entries {
		if id, ok := getIdFromPayload(entry.TextPayload); ok {
			count[id]++
		}
	}

	for i := 0; i < linesPerPod; i++ {
		_, ok := count[i]
		if !ok {
			missing = append(missing, i)
		}
	}

	return
}

func getIdFromPayload(payload string) (result int, ok bool) {
	chunks := strings.Split(payload, " ")
	if len(chunks) < 2 {
		return
	}

	if num, err := strconv.Atoi(chunks[1]); err != nil {
		ok = true
		result = num
	}

	return
}
