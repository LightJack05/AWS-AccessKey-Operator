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

// Package controller contains the reconciler and its tests.
// Integration tests require Docker — they start a SeaweedFS container as the
// IAM backend. Run them with: make test
package controller

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssdkconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	awsaccesskeyoperatorv1alpha1 "github.com/LightJack05/AWS-AccessKey-Operator/api/v1alpha1"
)

// Shared constants for the provider config used by all integration tests.
const (
	providerNamespace  = "aws-accesskey-operator-system"
	providerConfigName = "seaweedfs"
)

var (
	ctx       context.Context
	cancel    context.CancelFunc
	testEnv   *envtest.Environment
	cfg       *rest.Config
	k8sClient client.Client

	seaweedfsContainer testcontainers.Container
	seaweedfsEndpoint  string
	adminKeyID         string
	adminSecretKey     string
	adminIAMClient     *iam.Client
)

func TestControllers(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Controller Suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))
	ctx, cancel = context.WithCancel(context.TODO())

	// --- envtest ---
	By("bootstrapping test environment")
	Expect(awsaccesskeyoperatorv1alpha1.AddToScheme(scheme.Scheme)).To(Succeed())
	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}
	if dir := getFirstFoundEnvTestBinaryDir(); dir != "" {
		testEnv.BinaryAssetsDirectory = dir
	}
	var err error
	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())

	// --- SeaweedFS ---
	By("starting SeaweedFS container")
	seaweedfsContainer, adminKeyID, adminSecretKey = bootstrapSeaweedFS(ctx)
	mappedPort, err := seaweedfsContainer.MappedPort(ctx, "8333/tcp")
	Expect(err).NotTo(HaveOccurred())
	seaweedfsEndpoint = "http://localhost:" + mappedPort.Port()
	adminIAMClient = buildIAMClient(adminKeyID, adminSecretKey, seaweedfsEndpoint)

	// --- Shared Kubernetes resources ---
	By("creating shared provider namespace, admin secret, and IAMProviderConfig")
	Expect(k8sClient.Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: providerNamespace},
	})).To(Succeed())

	adminCreds := fmt.Sprintf(
		"[default]\naws_access_key_id = %s\naws_secret_access_key = %s\n",
		adminKeyID, adminSecretKey,
	)
	Expect(k8sClient.Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "seaweedfs-admin", Namespace: providerNamespace},
		Data:       map[string][]byte{"creds": []byte(adminCreds)},
	})).To(Succeed())

	Expect(k8sClient.Create(ctx, &awsaccesskeyoperatorv1alpha1.IAMProviderConfig{
		ObjectMeta: metav1.ObjectMeta{Name: providerConfigName, Namespace: providerNamespace},
		Spec: awsaccesskeyoperatorv1alpha1.IAMProviderConfigSpec{
			Endpoint:                  seaweedfsEndpoint,
			Region:                    "us-east-1",
			AdminCredentialsSecretRef: awsaccesskeyoperatorv1alpha1.IAMAdminCredentialsSecretRef{Name: "seaweedfs-admin"},
			AdminCredentialsSecretKey: "creds",
		},
	})).To(Succeed())

	// --- Manager ---
	By("starting controller manager")
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:                 scheme.Scheme,
		Metrics:                metricsserver.Options{BindAddress: "0"},
		HealthProbeBindAddress: "0",
		LeaderElection:         false,
	})
	Expect(err).NotTo(HaveOccurred())
	Expect((&IAMAccessKeyReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr)).To(Succeed())
	go func() {
		defer GinkgoRecover()
		Expect(mgr.Start(ctx)).To(Succeed())
	}()
})

var _ = AfterSuite(func() {
	By("stopping the manager and tearing down the test environment")
	cancel()
	Eventually(testEnv.Stop, time.Minute, time.Second).Should(Succeed())

	By("terminating SeaweedFS container")
	if seaweedfsContainer != nil {
		Expect(seaweedfsContainer.Terminate(context.Background())).To(Succeed())
	}
})

// bootstrapSeaweedFS starts a SeaweedFS container and seeds it with an admin
// identity via weed shell.  It mirrors the steps in devenv/startup.sh exactly:
// wait for both health endpoints, sleep 5 s, then run s3.configure via weed shell.
func bootstrapSeaweedFS(ctx context.Context) (testcontainers.Container, string, string) {
	keyID := strings.ToUpper(randomHex(10))
	secret := randomHex(20)

	req := testcontainers.ContainerRequest{
		Image:        "chrislusf/seaweedfs:latest",
		Cmd:          []string{"server", "-dir=/data", "-filer", "-s3", "-s3.iam.readOnly=false"},
		ExposedPorts: []string{"9333/tcp", "8888/tcp", "8333/tcp"},
		WaitingFor: wait.ForAll(
			wait.ForHTTP("/cluster/status").WithPort("9333/tcp").WithStartupTimeout(3*time.Minute),
			wait.ForHTTP("/healthz").WithPort("8888/tcp").WithStartupTimeout(3*time.Minute),
		),
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	Expect(err).NotTo(HaveOccurred())

	// The IAM subsystem initialises asynchronously after the health endpoints
	// respond.  A fixed sleep matches the devenv startup script.
	time.Sleep(5 * time.Second)

	// Seed the first admin identity via weed shell (the only bootstrap path).
	shellCmd := fmt.Sprintf(
		"echo 's3.configure -apply -user admin -access_key %s -secret_key %s -actions Admin' | weed shell",
		keyID, secret,
	)
	exitCode, reader, err := c.Exec(ctx, []string{"sh", "-c", shellCmd})
	Expect(err).NotTo(HaveOccurred())
	if reader != nil {
		out, _ := io.ReadAll(reader)
		GinkgoWriter.Printf("weed shell bootstrap: %s\n", out)
	}
	Expect(exitCode).To(Equal(0), "weed shell bootstrap exited non-zero")

	return c, keyID, secret
}

// buildIAMClient creates an AWS IAM client pointed at endpoint using static credentials.
func buildIAMClient(keyID, secret, endpoint string) *iam.Client {
	cfg, err := awssdkconfig.LoadDefaultConfig(context.Background(),
		awssdkconfig.WithRegion("us-east-1"),
		awssdkconfig.WithBaseEndpoint(endpoint),
		awssdkconfig.WithCredentialsProvider(aws.NewCredentialsCache(
			credentials.NewStaticCredentialsProvider(keyID, secret, ""),
		)),
	)
	Expect(err).NotTo(HaveOccurred())
	return iam.NewFromConfig(cfg)
}

// randomHex returns n random bytes encoded as a lowercase hex string.
func randomHex(n int) string {
	b := make([]byte, n)
	_, err := rand.Read(b)
	Expect(err).NotTo(HaveOccurred())
	return hex.EncodeToString(b)
}

// getFirstFoundEnvTestBinaryDir locates the first binary directory under bin/k8s,
// allowing the suite to be run from an IDE without setting KUBEBUILDER_ASSETS.
func getFirstFoundEnvTestBinaryDir() string {
	basePath := filepath.Join("..", "..", "bin", "k8s")
	entries, err := os.ReadDir(basePath)
	if err != nil {
		logf.Log.Error(err, "Failed to read directory", "path", basePath)
		return ""
	}
	for _, entry := range entries {
		if entry.IsDir() {
			return filepath.Join(basePath, entry.Name())
		}
	}
	return ""
}
