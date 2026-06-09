//go:build e2e
// +build e2e

/*
Copyright 2026.

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
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/LightJack05/AWS-AccessKey-Operator/test/utils"
)

// namespace where the project is deployed in
const namespace = "aws-accesskey-operator-system"

// serviceAccountName created for the project
const serviceAccountName = "aws-accesskey-operator-controller-manager"

// metricsServiceName is the name of the metrics service of the project
const metricsServiceName = "aws-accesskey-operator-controller-manager-metrics-service"

// metricsRoleBindingName is the name of the RBAC that will be created to allow get the metrics data
const metricsRoleBindingName = "aws-accesskey-operator-metrics-binding"

// seaweedfsNamespace is the namespace for the in-cluster SeaweedFS IAM backend.
// It is kept separate from the operator namespace so that SeaweedFS can run
// without the restricted PodSecurity policy applied to the operator namespace.
const seaweedfsNamespace = "seaweedfs-e2e"

var _ = Describe("Manager", Ordered, func() {
	var controllerPodName string

	// SeaweedFS state shared across the suite.
	var (
		seaweedfsPodName     string
		seaweedfsAdminKeyID  string
		seaweedfsAdminSecret string
	)

	BeforeAll(func() {
		By("creating manager namespace")
		cmd := exec.Command("kubectl", "create", "ns", namespace)
		_, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to create namespace")

		By("labeling the namespace to enforce the restricted security policy")
		cmd = exec.Command("kubectl", "label", "--overwrite", "ns", namespace,
			"pod-security.kubernetes.io/enforce=restricted")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to label namespace with restricted policy")

		By("installing CRDs")
		cmd = exec.Command("make", "install")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to install CRDs")

		By("deploying the controller-manager")
		cmd = exec.Command("make", "deploy", fmt.Sprintf("IMG=%s", managerImage))
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to deploy the controller-manager")

		// ---- SeaweedFS in-cluster setup ----

		By("creating SeaweedFS namespace")
		cmd = exec.Command("kubectl", "create", "ns", seaweedfsNamespace)
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to create SeaweedFS namespace")

		By("deploying SeaweedFS Deployment and Service")
		seaweedfsManifest := fmt.Sprintf(`apiVersion: apps/v1
kind: Deployment
metadata:
  name: seaweedfs
  namespace: %s
spec:
  selector:
    matchLabels:
      app: seaweedfs
  template:
    metadata:
      labels:
        app: seaweedfs
    spec:
      containers:
      - name: seaweedfs
        image: chrislusf/seaweedfs:latest
        args: ["server", "-dir=/data", "-filer", "-s3", "-s3.iam.readOnly=false"]
        ports:
        - name: master
          containerPort: 9333
        - name: filer
          containerPort: 8888
        - name: s3
          containerPort: 8333
        volumeMounts:
        - name: data
          mountPath: /data
      volumes:
      - name: data
        emptyDir: {}
---
apiVersion: v1
kind: Service
metadata:
  name: seaweedfs
  namespace: %s
spec:
  selector:
    app: seaweedfs
  ports:
  - name: s3
    port: 8333
    targetPort: s3
`, seaweedfsNamespace, seaweedfsNamespace)
		applyCmd := exec.Command("kubectl", "apply", "-f", "-")
		applyCmd.Stdin = strings.NewReader(seaweedfsManifest)
		_, err = utils.Run(applyCmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to deploy SeaweedFS")

		By("waiting for SeaweedFS Deployment to be available")
		cmd = exec.Command("kubectl", "wait", "deployment/seaweedfs",
			"--namespace", seaweedfsNamespace,
			"--for=condition=Available",
			"--timeout=5m",
		)
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "SeaweedFS deployment did not become available")

		By("locating the SeaweedFS pod")
		Eventually(func(g Gomega) {
			cmd := exec.Command("kubectl", "get", "pods",
				"--namespace", seaweedfsNamespace,
				"--selector=app=seaweedfs",
				"--field-selector=status.phase=Running",
				"-o", "jsonpath={.items[0].metadata.name}",
			)
			out, err := utils.Run(cmd)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(out).NotTo(BeEmpty())
			seaweedfsPodName = strings.TrimSpace(out)
		}).Should(Succeed())

		// Allow the IAM subsystem to finish initialising after the process
		// reports ready — mirrors the devenv startup.sh sleep.
		By("waiting for SeaweedFS IAM subsystem to initialise")
		time.Sleep(10 * time.Second)

		By("bootstrapping SeaweedFS admin credentials via weed shell")
		seaweedfsAdminKeyID = strings.ToUpper(randHex(10))
		seaweedfsAdminSecret = randHex(20)
		bootstrapCmd := fmt.Sprintf(
			"echo 's3.configure -apply -user admin -access_key %s -secret_key %s -actions Admin' | weed shell",
			seaweedfsAdminKeyID, seaweedfsAdminSecret,
		)
		Eventually(func(g Gomega) {
			cmd := exec.Command("kubectl", "exec", seaweedfsPodName,
				"--namespace", seaweedfsNamespace,
				"--", "sh", "-c", bootstrapCmd,
			)
			out, err := utils.Run(cmd)
			g.Expect(err).NotTo(HaveOccurred(), "weed shell bootstrap failed")
			g.Expect(out).To(ContainSubstring(seaweedfsAdminKeyID), "admin key not confirmed in weed shell output")
		}, 2*time.Minute, 5*time.Second).Should(Succeed())

		By("creating admin credentials Secret in operator namespace")
		adminSecretYAML := fmt.Sprintf(`apiVersion: v1
kind: Secret
metadata:
  name: seaweedfs-admin
  namespace: %s
stringData:
  creds: |
    [default]
    aws_access_key_id = %s
    aws_secret_access_key = %s
`, namespace, seaweedfsAdminKeyID, seaweedfsAdminSecret)
		applyCmd = exec.Command("kubectl", "apply", "-f", "-")
		applyCmd.Stdin = strings.NewReader(adminSecretYAML)
		_, err = utils.Run(applyCmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to create admin credentials Secret")

		By("applying IAMProviderConfig")
		providerConfigYAML := fmt.Sprintf(`apiVersion: aws-accesskey-operator.lightjack.de/v1alpha1
kind: IAMProviderConfig
metadata:
  name: seaweedfs
  namespace: %s
spec:
  endpoint: "http://seaweedfs.%s.svc:8333"
  region: us-east-1
  adminCredentialsSecretRef:
    name: seaweedfs-admin
  adminCredentialsSecretKey: creds
`, namespace, seaweedfsNamespace)
		applyCmd = exec.Command("kubectl", "apply", "-f", "-")
		applyCmd.Stdin = strings.NewReader(providerConfigYAML)
		_, err = utils.Run(applyCmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to apply IAMProviderConfig")
	})

	AfterAll(func() {
		By("cleaning up the curl pod for metrics")
		cmd := exec.Command("kubectl", "delete", "pod", "curl-metrics", "-n", namespace)
		_, _ = utils.Run(cmd)

		By("undeploying the controller-manager")
		cmd = exec.Command("make", "undeploy")
		_, _ = utils.Run(cmd)

		By("uninstalling CRDs")
		cmd = exec.Command("make", "uninstall")
		_, _ = utils.Run(cmd)

		By("removing SeaweedFS namespace")
		cmd = exec.Command("kubectl", "delete", "ns", seaweedfsNamespace, "--ignore-not-found")
		_, _ = utils.Run(cmd)

		By("removing manager namespace")
		cmd = exec.Command("kubectl", "delete", "ns", namespace)
		_, _ = utils.Run(cmd)
	})

	AfterEach(func() {
		specReport := CurrentSpecReport()
		if specReport.Failed() {
			By("Fetching controller manager pod logs")
			cmd := exec.Command("kubectl", "logs", controllerPodName, "-n", namespace)
			controllerLogs, err := utils.Run(cmd)
			if err == nil {
				_, _ = fmt.Fprintf(GinkgoWriter, "Controller logs:\n %s", controllerLogs)
			} else {
				_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get Controller logs: %s", err)
			}

			By("Fetching Kubernetes events")
			cmd = exec.Command("kubectl", "get", "events", "-n", namespace, "--sort-by=.lastTimestamp")
			eventsOutput, err := utils.Run(cmd)
			if err == nil {
				_, _ = fmt.Fprintf(GinkgoWriter, "Kubernetes events:\n%s", eventsOutput)
			} else {
				_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get Kubernetes events: %s", err)
			}

			By("Fetching curl-metrics logs")
			cmd = exec.Command("kubectl", "logs", "curl-metrics", "-n", namespace)
			metricsOutput, err := utils.Run(cmd)
			if err == nil {
				_, _ = fmt.Fprintf(GinkgoWriter, "Metrics logs:\n %s", metricsOutput)
			} else {
				_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get curl-metrics logs: %s", err)
			}

			By("Fetching controller manager pod description")
			cmd = exec.Command("kubectl", "describe", "pod", controllerPodName, "-n", namespace)
			podDescription, err := utils.Run(cmd)
			if err == nil {
				fmt.Println("Pod description:\n", podDescription)
			} else {
				fmt.Println("Failed to describe controller pod")
			}
		}
	})

	SetDefaultEventuallyTimeout(2 * time.Minute)
	SetDefaultEventuallyPollingInterval(time.Second)

	Context("Manager", func() {
		It("should run successfully", func() {
			By("validating that the controller-manager pod is running as expected")
			verifyControllerUp := func(g Gomega) {
				cmd := exec.Command("kubectl", "get",
					"pods", "-l", "control-plane=controller-manager",
					"-o", "go-template={{ range .items }}"+
						"{{ if not .metadata.deletionTimestamp }}"+
						"{{ .metadata.name }}"+
						"{{ \"\\n\" }}{{ end }}{{ end }}",
					"-n", namespace,
				)

				podOutput, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred(), "Failed to retrieve controller-manager pod information")
				podNames := utils.GetNonEmptyLines(podOutput)
				g.Expect(podNames).To(HaveLen(1), "expected 1 controller pod running")
				controllerPodName = podNames[0]
				g.Expect(controllerPodName).To(ContainSubstring("controller-manager"))

				cmd = exec.Command("kubectl", "get",
					"pods", controllerPodName, "-o", "jsonpath={.status.phase}",
					"-n", namespace,
				)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("Running"), "Incorrect controller-manager pod status")
			}
			Eventually(verifyControllerUp).Should(Succeed())
		})

		It("should ensure the metrics endpoint is serving metrics", func() {
			By("creating a ClusterRoleBinding for the service account to allow access to metrics")
			cmd := exec.Command("kubectl", "create", "clusterrolebinding", metricsRoleBindingName,
				"--clusterrole=aws-accesskey-operator-metrics-reader",
				fmt.Sprintf("--serviceaccount=%s:%s", namespace, serviceAccountName),
			)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to create ClusterRoleBinding")

			By("validating that the metrics service is available")
			cmd = exec.Command("kubectl", "get", "service", metricsServiceName, "-n", namespace)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Metrics service should exist")

			By("getting the service account token")
			token, err := serviceAccountToken()
			Expect(err).NotTo(HaveOccurred())
			Expect(token).NotTo(BeEmpty())

			By("ensuring the controller pod is ready")
			verifyControllerPodReady := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "pod", controllerPodName, "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("True"), "Controller pod not ready")
			}
			Eventually(verifyControllerPodReady, 3*time.Minute, time.Second).Should(Succeed())

			By("verifying that the controller manager is serving the metrics server")
			verifyMetricsServerStarted := func(g Gomega) {
				cmd := exec.Command("kubectl", "logs", controllerPodName, "-n", namespace)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(ContainSubstring("Serving metrics server"),
					"Metrics server not yet started")
			}
			Eventually(verifyMetricsServerStarted, 3*time.Minute, time.Second).Should(Succeed())

			// +kubebuilder:scaffold:e2e-metrics-webhooks-readiness

			By("creating the curl-metrics pod to access the metrics endpoint")
			cmd = exec.Command("kubectl", "run", "curl-metrics", "--restart=Never",
				"--namespace", namespace,
				"--image=curlimages/curl:latest",
				"--overrides",
				fmt.Sprintf(`{
					"spec": {
						"containers": [{
							"name": "curl",
							"image": "curlimages/curl:latest",
							"command": ["/bin/sh", "-c"],
							"args": [
								"for i in $(seq 1 30); do curl -v -k -H 'Authorization: Bearer %s' https://%s.%s.svc.cluster.local:8443/metrics && exit 0 || sleep 2; done; exit 1"
							],
							"securityContext": {
								"readOnlyRootFilesystem": true,
								"allowPrivilegeEscalation": false,
								"capabilities": {
									"drop": ["ALL"]
								},
								"runAsNonRoot": true,
								"runAsUser": 1000,
								"seccompProfile": {
									"type": "RuntimeDefault"
								}
							}
						}],
						"serviceAccountName": "%s"
					}
				}`, token, metricsServiceName, namespace, serviceAccountName))
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to create curl-metrics pod")

			By("waiting for the curl-metrics pod to complete.")
			verifyCurlUp := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "pods", "curl-metrics",
					"-o", "jsonpath={.status.phase}",
					"-n", namespace)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("Succeeded"), "curl pod in wrong status")
			}
			Eventually(verifyCurlUp, 5*time.Minute).Should(Succeed())

			By("getting the metrics by checking curl-metrics logs")
			verifyMetricsAvailable := func(g Gomega) {
				metricsOutput, err := getMetricsOutput()
				g.Expect(err).NotTo(HaveOccurred(), "Failed to retrieve logs from curl pod")
				g.Expect(metricsOutput).NotTo(BeEmpty())
				g.Expect(metricsOutput).To(ContainSubstring("< HTTP/1.1 200 OK"))
			}
			Eventually(verifyMetricsAvailable, 2*time.Minute).Should(Succeed())
		})

		// +kubebuilder:scaffold:e2e-webhooks-checks
	})

	// -------------------------------------------------------------------------
	// IAMAccessKey contract scenarios
	// -------------------------------------------------------------------------

	Context("IAMAccessKey", func() {
		var (
			testNs  string
			iamUser string
		)

		BeforeEach(func() {
			testNs = "e2e-" + randHex(4)
			iamUser = "e2e-user-" + randHex(4)

			By(fmt.Sprintf("creating test namespace %s", testNs))
			cmd := exec.Command("kubectl", "create", "namespace", testNs)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By(fmt.Sprintf("seeding IAM user %s via weed shell", iamUser))
			seedIAMUser(seaweedfsPodName, iamUser)
		})

		AfterEach(func() {
			By(fmt.Sprintf("cleaning up IAM user %s", iamUser))
			cleanupIAMUser(seaweedfsPodName, iamUser)

			By(fmt.Sprintf("deleting test namespace %s", testNs))
			cmd := exec.Command("kubectl", "delete", "namespace", testNs, "--ignore-not-found")
			_, _ = utils.Run(cmd)
		})

		It("creates a credentials Secret and sets Ready=True for a valid IAMAccessKey", func() {
			By("applying IAMProviderGrant and IAMAccessKey")
			applyTestResources(testNs, iamUser, "test-key", "output-creds")

			By("waiting for Ready=True (AlreadyExists — the stable post-creation reason)")
			Eventually(func(g Gomega) {
				status, _ := readyConditionStatus(testNs, "test-key")
				g.Expect(status).To(Equal("True"))
			}, 2*time.Minute, time.Second).Should(Succeed())

			By("verifying the output Secret contains valid AWS INI credentials")
			secretData := readSecretCredentials(testNs, "output-creds")
			Expect(secretData).To(ContainSubstring("aws_access_key_id"))
			Expect(secretData).To(ContainSubstring("aws_secret_access_key"))
			Expect(secretData).To(ContainSubstring("[default]"))
		})

		It("re-creates the credentials Secret with fresh keys when the Secret is deleted (rotation)", func() {
			By("applying IAMProviderGrant and IAMAccessKey")
			applyTestResources(testNs, iamUser, "test-key", "output-creds")

			By("waiting for the initial Secret to be stable")
			Eventually(func(g Gomega) {
				status, _ := readyConditionStatus(testNs, "test-key")
				g.Expect(status).To(Equal("True"))
			}, 2*time.Minute, time.Second).Should(Succeed())

			originalKeyID := extractKeyIDFromSecret(testNs, "output-creds")
			Expect(originalKeyID).NotTo(BeEmpty())

			By("deleting the output Secret to trigger rotation")
			cmd := exec.Command("kubectl", "delete", "secret", "output-creds", "-n", testNs)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for a new Secret with a different access key ID")
			Eventually(func(g Gomega) {
				newKeyID := extractKeyIDFromSecret(testNs, "output-creds")
				g.Expect(newKeyID).NotTo(BeEmpty())
				g.Expect(newKeyID).NotTo(Equal(originalKeyID))
			}, 2*time.Minute, time.Second).Should(Succeed())

			By("confirming the IAMAccessKey returns to Ready=True after rotation")
			Eventually(func(g Gomega) {
				status, _ := readyConditionStatus(testNs, "test-key")
				g.Expect(status).To(Equal("True"))
			}, 2*time.Minute, time.Second).Should(Succeed())
		})

		It("deletes the output Secret and sets GrantDenied when the IAMProviderGrant is revoked", func() {
			By("applying IAMProviderGrant and IAMAccessKey")
			applyTestResources(testNs, iamUser, "test-key", "output-creds")

			By("waiting for initial Ready=True")
			Eventually(func(g Gomega) {
				status, _ := readyConditionStatus(testNs, "test-key")
				g.Expect(status).To(Equal("True"))
			}, 2*time.Minute, time.Second).Should(Succeed())

			By("removing the username from the IAMProviderGrant")
			// Patch allowedUsernames to an empty-looking list by replacing the grant
			// with a different username so our user is no longer covered.
			patchedGrantYAML := fmt.Sprintf(`apiVersion: aws-accesskey-operator.lightjack.de/v1alpha1
kind: IAMProviderGrant
metadata:
  name: allow-seaweedfs
  namespace: %s
spec:
  providerConfigRef:
    name: seaweedfs
    namespace: %s
  allowedUsernames:
  - other-user
`, testNs, namespace)
			applyCmd := exec.Command("kubectl", "apply", "-f", "-")
			applyCmd.Stdin = strings.NewReader(patchedGrantYAML)
			_, err := utils.Run(applyCmd)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for Ready=False with reason GrantDenied")
			Eventually(func(g Gomega) {
				status, reason := readyConditionStatus(testNs, "test-key")
				g.Expect(status).To(Equal("False"))
				g.Expect(reason).To(Equal("GrantDenied"))
			}, 2*time.Minute, time.Second).Should(Succeed())

			By("confirming the output Secret is deleted after grant revocation")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "secret", "output-creds", "-n", testNs)
				_, err := utils.Run(cmd)
				g.Expect(err).To(HaveOccurred(), "Secret should have been deleted")
			}, 2*time.Minute, time.Second).Should(Succeed())
		})

		It("garbage-collects the output Secret when the IAMAccessKey is deleted", func() {
			By("applying IAMProviderGrant and IAMAccessKey")
			applyTestResources(testNs, iamUser, "test-key", "output-creds")

			By("waiting for the output Secret to exist")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "secret", "output-creds", "-n", testNs)
				_, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
			}, 2*time.Minute, time.Second).Should(Succeed())

			By("deleting the IAMAccessKey resource")
			cmd := exec.Command("kubectl", "delete", "iamaccesskey", "test-key", "-n", testNs)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("confirming Kubernetes garbage-collects the owned Secret")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "secret", "output-creds", "-n", testNs)
				_, err := utils.Run(cmd)
				g.Expect(err).To(HaveOccurred(), "Secret should have been garbage-collected")
			}, 2*time.Minute, time.Second).Should(Succeed())
		})
	})
})

// -------------------------------------------------------------------------
// Helpers — scaffolded metrics tests
// -------------------------------------------------------------------------

// serviceAccountToken returns a token for the specified service account in the given namespace.
// It uses the Kubernetes TokenRequest API to generate a token by directly sending a request
// and parsing the resulting token from the API response.
func serviceAccountToken() (string, error) {
	const tokenRequestRawString = `{
		"apiVersion": "authentication.k8s.io/v1",
		"kind": "TokenRequest"
	}`

	// Temporary file to store the token request
	secretName := fmt.Sprintf("%s-token-request", serviceAccountName)
	tokenRequestFile := filepath.Join("/tmp", secretName)
	err := os.WriteFile(tokenRequestFile, []byte(tokenRequestRawString), os.FileMode(0o644))
	if err != nil {
		return "", err
	}

	var out string
	verifyTokenCreation := func(g Gomega) {
		// Execute kubectl command to create the token
		cmd := exec.Command("kubectl", "create", "--raw", fmt.Sprintf(
			"/api/v1/namespaces/%s/serviceaccounts/%s/token",
			namespace,
			serviceAccountName,
		), "-f", tokenRequestFile)

		output, err := cmd.CombinedOutput()
		g.Expect(err).NotTo(HaveOccurred())

		// Parse the JSON output to extract the token
		var token tokenRequest
		err = json.Unmarshal(output, &token)
		g.Expect(err).NotTo(HaveOccurred())

		out = token.Status.Token
	}
	Eventually(verifyTokenCreation).Should(Succeed())

	return out, err
}

// getMetricsOutput retrieves and returns the logs from the curl pod used to access the metrics endpoint.
func getMetricsOutput() (string, error) {
	By("getting the curl-metrics logs")
	cmd := exec.Command("kubectl", "logs", "curl-metrics", "-n", namespace)
	return utils.Run(cmd)
}

// tokenRequest is a simplified representation of the Kubernetes TokenRequest API response,
// containing only the token field that we need to extract.
type tokenRequest struct {
	Status struct {
		Token string `json:"token"`
	} `json:"status"`
}

// -------------------------------------------------------------------------
// Helpers — IAMAccessKey e2e scenarios
// -------------------------------------------------------------------------

// applyTestResources creates an IAMProviderGrant and IAMAccessKey in testNs.
func applyTestResources(testNs, iamUser, accessKeyName, secretName string) {
	GinkgoHelper()
	manifest := fmt.Sprintf(`apiVersion: aws-accesskey-operator.lightjack.de/v1alpha1
kind: IAMProviderGrant
metadata:
  name: allow-seaweedfs
  namespace: %s
spec:
  providerConfigRef:
    name: seaweedfs
    namespace: %s
  allowedUsernames:
  - %s
---
apiVersion: aws-accesskey-operator.lightjack.de/v1alpha1
kind: IAMAccessKey
metadata:
  name: %s
  namespace: %s
spec:
  providerConfigRef:
    name: seaweedfs
    namespace: %s
  username: %s
  secretName: %s
  secretField: credentials
`, testNs, namespace, iamUser,
		accessKeyName, testNs, namespace, iamUser, secretName)

	cmd := exec.Command("kubectl", "apply", "-f", "-")
	cmd.Stdin = strings.NewReader(manifest)
	_, err := utils.Run(cmd)
	Expect(err).NotTo(HaveOccurred())
}

// readyConditionStatus returns the Ready condition status and reason for an IAMAccessKey.
func readyConditionStatus(ns, name string) (status, reason string) {
	statusCmd := exec.Command("kubectl", "get", "iamaccesskey", name, "-n", ns,
		"-o", `jsonpath={.status.conditions[?(@.type=="Ready")].status}`)
	status, _ = utils.Run(statusCmd)
	status = strings.TrimSpace(status)

	reasonCmd := exec.Command("kubectl", "get", "iamaccesskey", name, "-n", ns,
		"-o", `jsonpath={.status.conditions[?(@.type=="Ready")].reason}`)
	reason, _ = utils.Run(reasonCmd)
	reason = strings.TrimSpace(reason)
	return status, reason
}

// readSecretCredentials returns the decoded credentials field from an output Secret.
func readSecretCredentials(ns, secretName string) string {
	cmd := exec.Command("kubectl", "get", "secret", secretName, "-n", ns,
		"-o", "jsonpath={.data.credentials}")
	b64, err := utils.Run(cmd)
	if err != nil {
		return ""
	}
	data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
	if err != nil {
		return ""
	}
	return string(data)
}

// extractKeyIDFromSecret parses the aws_access_key_id from an output Secret.
func extractKeyIDFromSecret(ns, secretName string) string {
	creds := readSecretCredentials(ns, secretName)
	for _, line := range strings.Split(creds, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "aws_access_key_id") {
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				return strings.TrimSpace(parts[1])
			}
		}
	}
	return ""
}

// seedIAMUser registers a test IAM user in SeaweedFS via weed shell s3.configure.
// A placeholder access key is provided so the user lands in the credential store
// that CreateAccessKey consults.  -actions Admin ensures GetUser validation works.
func seedIAMUser(podName, username string) {
	GinkgoHelper()
	placeholderKey := strings.ToUpper(randHex(10))
	placeholderSecret := randHex(20)
	cmd := fmt.Sprintf(
		"echo 's3.configure -apply -user %s -access_key %s -secret_key %s -actions Admin' | weed shell",
		username, placeholderKey, placeholderSecret,
	)
	execCmd := exec.Command("kubectl", "exec", podName,
		"--namespace", seaweedfsNamespace,
		"--", "sh", "-c", cmd,
	)
	out, err := utils.Run(execCmd)
	Expect(err).NotTo(HaveOccurred(), "failed to seed IAM user %s", username)
	Expect(out).To(ContainSubstring(username), "user %s not confirmed in weed shell output", username)
}

// cleanupIAMUser removes a test IAM user from SeaweedFS via weed shell.
func cleanupIAMUser(podName, username string) {
	cmd := fmt.Sprintf("echo 's3.configure -delete -user %s' | weed shell", username)
	execCmd := exec.Command("kubectl", "exec", podName,
		"--namespace", seaweedfsNamespace,
		"--", "sh", "-c", cmd,
	)
	_, _ = utils.Run(execCmd)
}

// randHex returns n random bytes encoded as a lowercase hex string.
func randHex(n int) string {
	b := make([]byte, n)
	_, err := rand.Read(b)
	Expect(err).NotTo(HaveOccurred())
	return hex.EncodeToString(b)
}
