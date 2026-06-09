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

package controller

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	awsaccesskeyoperatorv1alpha1 "github.com/LightJack05/AWS-AccessKey-Operator/api/v1alpha1"
)

// reconcileTimeout is the maximum time to wait for the controller to react to a change.
const reconcileTimeout = 30 * time.Second
const credentialsKey = "credentials"

var _ = Describe("IAMAccessKey controller", func() {
	var (
		testNs  *corev1.Namespace
		iamUser string
	)

	BeforeEach(func() {
		testNs = &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "test-"}}
		Expect(k8sClient.Create(ctx, testNs)).To(Succeed())

		iamUser = "test-" + randomHex(4)
		// Seed the user via weed shell s3.configure WITH an explicit placeholder
		// access key, mirroring how the admin identity is bootstrapped.  Using
		// the IAM CreateUser API alone is insufficient: SeaweedFS only registers
		// users in the credential store that CreateAccessKey/ListAccessKeys
		// consult when they are created through s3.configure with credentials.
		// The placeholder key will be found and deleted by the operator's
		// clearAccessKeysForuser before the operator issues its own key.
		seedUserViaWeedShell(ctx, iamUser)
	})

	AfterEach(func() {
		cleanupIAMUser(ctx, iamUser)
		// Namespace deletion cascades to all namespaced resources.
		_ = k8sClient.Delete(ctx, testNs)
	})

	// -------------------------------------------------------------------------
	// Grant / permission
	// -------------------------------------------------------------------------

	Describe("grant permission", func() {
		It("sets Ready=False (GrantDenied) and creates no Secret when no IAMProviderGrant exists", func() {
			ak := makeAccessKey(testNs.Name, "ak", iamUser, "output-creds")
			Expect(k8sClient.Create(ctx, ak)).To(Succeed())

			expectCondition(ctx, client.ObjectKeyFromObject(ak), metav1.ConditionFalse, "GrantDenied")

			secret := &corev1.Secret{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNs.Name, Name: "output-creds"}, secret)).
				To(MatchError(ContainSubstring("not found")))
		})

		It("sets Ready=False (GrantDenied) when the grant does not include the requested username", func() {
			grant := makeGrant(testNs.Name, "grant", providerConfigName, providerNamespace, "other-user")
			Expect(k8sClient.Create(ctx, grant)).To(Succeed())

			ak := makeAccessKey(testNs.Name, "ak", iamUser, "output-creds")
			Expect(k8sClient.Create(ctx, ak)).To(Succeed())

			expectCondition(ctx, client.ObjectKeyFromObject(ak), metav1.ConditionFalse, "GrantDenied")
		})

		It("sets Ready=False (GrantDenied) when the grant references a different provider", func() {
			grant := makeGrant(testNs.Name, "grant", "other-config", providerNamespace, iamUser)
			Expect(k8sClient.Create(ctx, grant)).To(Succeed())

			ak := makeAccessKey(testNs.Name, "ak", iamUser, "output-creds")
			Expect(k8sClient.Create(ctx, ak)).To(Succeed())

			expectCondition(ctx, client.ObjectKeyFromObject(ak), metav1.ConditionFalse, "GrantDenied")
		})

		It("transitions to Ready=True once a matching IAMProviderGrant is created", func() {
			ak := makeAccessKey(testNs.Name, "ak", iamUser, "output-creds")
			Expect(k8sClient.Create(ctx, ak)).To(Succeed())
			expectCondition(ctx, client.ObjectKeyFromObject(ak), metav1.ConditionFalse, "GrantDenied")

			// Adding the grant triggers re-reconciliation via the IAMProviderGrant watch.
			grant := makeGrant(testNs.Name, "grant", providerConfigName, providerNamespace, iamUser)
			Expect(k8sClient.Create(ctx, grant)).To(Succeed())

			expectCondition(ctx, client.ObjectKeyFromObject(ak), metav1.ConditionTrue, "")
		})

		It("deletes the output Secret and sets GrantDenied when the grant is deleted", func() {
			grant := makeGrant(testNs.Name, "grant", providerConfigName, providerNamespace, iamUser)
			Expect(k8sClient.Create(ctx, grant)).To(Succeed())
			ak := makeAccessKey(testNs.Name, "ak", iamUser, "output-creds")
			Expect(k8sClient.Create(ctx, ak)).To(Succeed())
			expectCondition(ctx, client.ObjectKeyFromObject(ak), metav1.ConditionTrue, "")
			expectSecretExists(ctx, testNs.Name, "output-creds")

			Expect(k8sClient.Delete(ctx, grant)).To(Succeed())

			expectCondition(ctx, client.ObjectKeyFromObject(ak), metav1.ConditionFalse, "GrantDenied")
			expectSecretAbsent(ctx, testNs.Name, "output-creds")
		})

		It("deletes the output Secret and sets GrantDenied when the username is removed from the grant", func() {
			grant := makeGrant(testNs.Name, "grant", providerConfigName, providerNamespace, iamUser, "other-user")
			Expect(k8sClient.Create(ctx, grant)).To(Succeed())
			ak := makeAccessKey(testNs.Name, "ak", iamUser, "output-creds")
			Expect(k8sClient.Create(ctx, ak)).To(Succeed())
			expectCondition(ctx, client.ObjectKeyFromObject(ak), metav1.ConditionTrue, "")

			// Remove iamUser from the grant, keeping only "other-user".
			patch := client.MergeFrom(grant.DeepCopy())
			grant.Spec.AllowedUsernames = []string{"other-user"}
			Expect(k8sClient.Patch(ctx, grant, patch)).To(Succeed())

			expectCondition(ctx, client.ObjectKeyFromObject(ak), metav1.ConditionFalse, "GrantDenied")
			expectSecretAbsent(ctx, testNs.Name, "output-creds")
		})
	})

	// -------------------------------------------------------------------------
	// ProviderConfig resolution
	// -------------------------------------------------------------------------

	Describe("provider config resolution", func() {
		It("sets Ready=False (ProviderConfigNotFound) when the referenced IAMProviderConfig does not exist", func() {
			// The grant and the access key must reference the SAME (missing) provider
			// so the grant check passes and the config-not-found path is exercised.
			grant := makeGrant(testNs.Name, "grant", "missing-config", providerNamespace, iamUser)
			Expect(k8sClient.Create(ctx, grant)).To(Succeed())
			ak := makeAccessKey(testNs.Name, "ak", iamUser, "output-creds")
			ak.Spec.ProviderConfigRef.Name = "missing-config"
			Expect(k8sClient.Create(ctx, ak)).To(Succeed())

			expectCondition(ctx, client.ObjectKeyFromObject(ak), metav1.ConditionFalse, "ProviderConfigNotFound")
		})
	})

	// -------------------------------------------------------------------------
	// Key issuance
	// -------------------------------------------------------------------------

	Describe("key issuance", func() {
		It("creates a Secret with valid AWS INI credentials when all prerequisites are met", func() {
			setupValidPrerequisites(ctx, testNs.Name, iamUser)
			ak := makeAccessKey(testNs.Name, "ak", iamUser, "output-creds")
			Expect(k8sClient.Create(ctx, ak)).To(Succeed())

			expectCondition(ctx, client.ObjectKeyFromObject(ak), metav1.ConditionTrue, "")

			secret := &corev1.Secret{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNs.Name, Name: "output-creds"}, secret)).
				To(Succeed())
			Expect(secret.Data).To(HaveKey(credentialsKey))

			expectCredentialsWork(Default, ctx, secret, iamUser)
		})

		It("re-creates the Secret with fresh credentials when the output Secret is deleted", func() {
			setupValidPrerequisites(ctx, testNs.Name, iamUser)
			ak := makeAccessKey(testNs.Name, "ak", iamUser, "output-creds")
			Expect(k8sClient.Create(ctx, ak)).To(Succeed())
			expectCondition(ctx, client.ObjectKeyFromObject(ak), metav1.ConditionTrue, "")

			// Record the original access key ID.
			original := &corev1.Secret{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNs.Name, Name: "output-creds"}, original)).
				To(Succeed())
			originalKeyID := extractKeyID(original)

			// Delete the Secret — the Secret watch triggers re-reconciliation.
			Expect(k8sClient.Delete(ctx, original)).To(Succeed())

			// Wait for the new Secret to appear with different credentials.
			Eventually(func(g Gomega) {
				fresh := &corev1.Secret{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNs.Name, Name: "output-creds"}, fresh)).
					To(Succeed())
				g.Expect(extractKeyID(fresh)).NotTo(Equal(originalKeyID))
				expectCredentialsWork(g, ctx, fresh, iamUser)
			}, reconcileTimeout, 250*time.Millisecond).Should(Succeed())
		})
	})

	// -------------------------------------------------------------------------
	// Secret ownership and conflict
	// -------------------------------------------------------------------------

	Describe("secret ownership", func() {
		It("sets Ready=False (SecretConflict) when a Secret with the target name exists but is not owned by this IAMAccessKey", func() {
			setupValidPrerequisites(ctx, testNs.Name, iamUser)

			// Pre-create a Secret with the target name and no owner reference.
			preExisting := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "output-creds", Namespace: testNs.Name},
				Data:       map[string][]byte{credentialsKey: []byte("not-my-secret")},
			}
			Expect(k8sClient.Create(ctx, preExisting)).To(Succeed())

			ak := makeAccessKey(testNs.Name, "ak", iamUser, "output-creds")
			Expect(k8sClient.Create(ctx, ak)).To(Succeed())

			expectCondition(ctx, client.ObjectKeyFromObject(ak), metav1.ConditionFalse, "SecretConflict")

			// The pre-existing Secret must not have been modified.
			unchanged := &corev1.Secret{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNs.Name, Name: "output-creds"}, unchanged)).
				To(Succeed())
			Expect(unchanged.Data[credentialsKey]).To(Equal([]byte("not-my-secret")))
		})

		It("re-creates the Secret when it is owned but contains unparseable INI data", func() {
			setupValidPrerequisites(ctx, testNs.Name, iamUser)
			ak := makeAccessKey(testNs.Name, "ak", iamUser, "output-creds")
			Expect(k8sClient.Create(ctx, ak)).To(Succeed())
			expectCondition(ctx, client.ObjectKeyFromObject(ak), metav1.ConditionTrue, "")

			// Overwrite the credentials field with invalid INI.  The Secret watch
			// fires and the controller re-evaluates the Secret.
			secret := &corev1.Secret{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNs.Name, Name: "output-creds"}, secret)).
				To(Succeed())
			patch := client.MergeFrom(secret.DeepCopy())
			secret.Data[credentialsKey] = []byte("not-valid-ini")
			Expect(k8sClient.Patch(ctx, secret, patch)).To(Succeed())

			// Wait for the controller to detect the invalid data, delete, and re-issue.
			Eventually(func(g Gomega) {
				fresh := &corev1.Secret{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNs.Name, Name: "output-creds"}, fresh)).
					To(Succeed())
				expectCredentialsWork(g, ctx, fresh, iamUser)
			}, reconcileTimeout, 250*time.Millisecond).Should(Succeed())
		})

		It("re-creates the Secret when it is owned but its credentials are rejected by GetUser", func() {
			setupValidPrerequisites(ctx, testNs.Name, iamUser)
			ak := makeAccessKey(testNs.Name, "ak", iamUser, "output-creds")
			Expect(k8sClient.Create(ctx, ak)).To(Succeed())
			expectCondition(ctx, client.ObjectKeyFromObject(ak), metav1.ConditionTrue, "")

			// Delete the IAM access key on SeaweedFS so the stored credentials fail
			// GetUser validation.
			listOut, err := adminIAMClient.ListAccessKeys(ctx, &iam.ListAccessKeysInput{UserName: aws.String(iamUser)})
			Expect(err).NotTo(HaveOccurred())
			for _, key := range listOut.AccessKeyMetadata {
				_, err := adminIAMClient.DeleteAccessKey(ctx, &iam.DeleteAccessKeyInput{
					UserName:    aws.String(iamUser),
					AccessKeyId: key.AccessKeyId,
				})
				Expect(err).NotTo(HaveOccurred())
			}

			// Touch the Secret to trigger its watch and force a reconcile.
			secret := &corev1.Secret{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNs.Name, Name: "output-creds"}, secret)).
				To(Succeed())
			patch := client.MergeFrom(secret.DeepCopy())
			if secret.Annotations == nil {
				secret.Annotations = map[string]string{}
			}
			secret.Annotations["test/force-reconcile"] = fmt.Sprintf("%d", time.Now().UnixNano())
			Expect(k8sClient.Patch(ctx, secret, patch)).To(Succeed())

			// The controller detects the GetUser failure, deletes the Secret,
			// and re-creates with fresh credentials.
			Eventually(func(g Gomega) {
				fresh := &corev1.Secret{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNs.Name, Name: "output-creds"}, fresh)).
					To(Succeed())
				expectCredentialsWork(g, ctx, fresh, iamUser)
			}, reconcileTimeout, 250*time.Millisecond).Should(Succeed())
		})
	})
})

// -------------------------------------------------------------------------
// Helpers
// -------------------------------------------------------------------------

// makeAccessKey builds an IAMAccessKey pointing at the shared IAMProviderConfig.
func makeAccessKey(ns, name, username, secretName string) *awsaccesskeyoperatorv1alpha1.IAMAccessKey {
	return &awsaccesskeyoperatorv1alpha1.IAMAccessKey{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: awsaccesskeyoperatorv1alpha1.IAMAccessKeySpec{
			ProviderConfigRef: awsaccesskeyoperatorv1alpha1.IAMProviderConfigRef{
				Name:      providerConfigName,
				Namespace: providerNamespace,
			},
			Username:    username,
			SecretName:  secretName,
			SecretField: credentialsKey,
		},
	}
}

// makeGrant builds an IAMProviderGrant allowing one or more usernames.
func makeGrant(ns, name, configName, configNs string, usernames ...string) *awsaccesskeyoperatorv1alpha1.IAMProviderGrant {
	return &awsaccesskeyoperatorv1alpha1.IAMProviderGrant{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: awsaccesskeyoperatorv1alpha1.IAMProviderGrantSpec{
			ProviderConfigRef: awsaccesskeyoperatorv1alpha1.IAMProviderConfigRef{
				Name:      configName,
				Namespace: configNs,
			},
			AllowedUsernames: usernames,
		},
	}
}

// setupValidPrerequisites creates the IAMProviderGrant required for a successful
// reconcile.  Call this before creating the IAMAccessKey in tests that need a
// happy-path setup.
func setupValidPrerequisites(ctx context.Context, ns, iamUser string) {
	GinkgoHelper()
	grant := makeGrant(ns, "grant", providerConfigName, providerNamespace, iamUser)
	Expect(k8sClient.Create(ctx, grant)).To(Succeed())
}

// expectCondition waits until the IAMAccessKey's Ready condition matches status
// and, if reason is non-empty, also matches the reason.
func expectCondition(ctx context.Context, key client.ObjectKey, status metav1.ConditionStatus, reason string) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		ak := &awsaccesskeyoperatorv1alpha1.IAMAccessKey{}
		g.Expect(k8sClient.Get(ctx, key, ak)).To(Succeed())
		cond := apimeta.FindStatusCondition(ak.Status.Conditions, awsaccesskeyoperatorv1alpha1.ConditionReady)
		g.Expect(cond).NotTo(BeNil())
		g.Expect(cond.Status).To(Equal(status))
		if reason != "" {
			g.Expect(cond.Reason).To(Equal(reason))
		}
	}, reconcileTimeout, 250*time.Millisecond).Should(Succeed())
}

// expectSecretExists asserts the Secret is present within reconcileTimeout.
func expectSecretExists(ctx context.Context, ns, name string) {
	GinkgoHelper()
	Eventually(func() error {
		return k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: name}, &corev1.Secret{})
	}, reconcileTimeout, 250*time.Millisecond).Should(Succeed())
}

// expectSecretAbsent asserts the Secret disappears within reconcileTimeout.
func expectSecretAbsent(ctx context.Context, ns, name string) {
	GinkgoHelper()
	Eventually(func() bool {
		err := k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: name}, &corev1.Secret{})
		return err != nil
	}, reconcileTimeout, 250*time.Millisecond).Should(BeTrue())
}

// expectCredentialsWork parses the Secret's credentials field and verifies
// they are accepted by SeaweedFS by calling iam.GetUser for the IAM username.
// This mirrors the controller's own validation logic; STS GetCallerIdentity is
// not used because it is unreliable for dynamically-created keys in SeaweedFS.
//
// g must be the Gomega instance from the enclosing Eventually block (or Default
// for direct call sites) so that failures are handled appropriately.
func expectCredentialsWork(g Gomega, ctx context.Context, secret *corev1.Secret, username string) {
	GinkgoHelper()
	providerConfig := &awsaccesskeyoperatorv1alpha1.IAMProviderConfig{}
	g.Expect(k8sClient.Get(ctx, types.NamespacedName{
		Namespace: providerNamespace,
		Name:      providerConfigName,
	}, providerConfig)).To(Succeed())

	awsCfg, err := loadAWSConfigFromString(ctx, string(secret.Data[credentialsKey]), providerConfig)
	g.Expect(err).NotTo(HaveOccurred())

	iamClient := iam.NewFromConfig(awsCfg)
	_, err = iamClient.GetUser(ctx, &iam.GetUserInput{UserName: aws.String(username)})
	g.Expect(err).NotTo(HaveOccurred())
}

// extractKeyID parses the aws_access_key_id from a credentials Secret.
func extractKeyID(secret *corev1.Secret) string {
	GinkgoHelper()
	providerConfig := &awsaccesskeyoperatorv1alpha1.IAMProviderConfig{}
	Expect(k8sClient.Get(ctx, types.NamespacedName{
		Namespace: providerNamespace,
		Name:      providerConfigName,
	}, providerConfig)).To(Succeed())

	awsCfg, err := loadAWSConfigFromString(ctx, string(secret.Data[credentialsKey]), providerConfig)
	Expect(err).NotTo(HaveOccurred())
	creds, err := awsCfg.Credentials.Retrieve(ctx)
	Expect(err).NotTo(HaveOccurred())
	return creds.AccessKeyID
}

// seedUserViaWeedShell creates a test IAM user in SeaweedFS using the same
// weed-shell s3.configure path as the admin bootstrap.  A random placeholder
// access key is provided so the user lands in the credential store that
// ListAccessKeys and CreateAccessKey consult.  The operator's
// clearAccessKeysForuser will delete this placeholder key before issuing its
// own.  -actions Admin ensures IAM GetUser validation works for resulting keys.
func seedUserViaWeedShell(ctx context.Context, username string) {
	GinkgoHelper()
	placeholderKeyID := strings.ToUpper(randomHex(10))
	placeholderSecret := randomHex(20)
	cmd := fmt.Sprintf(
		"echo 's3.configure -apply -user %s -access_key %s -secret_key %s -actions Admin' | weed shell",
		username, placeholderKeyID, placeholderSecret,
	)
	exitCode, reader, err := seaweedfsContainer.Exec(ctx, []string{"sh", "-c", cmd})
	Expect(err).NotTo(HaveOccurred())
	if reader != nil {
		out, _ := io.ReadAll(reader)
		GinkgoWriter.Printf("seed user %s: %s\n", username, out)
	}
	Expect(exitCode).To(Equal(0), "s3.configure seed for user %s exited non-zero", username)
}

// cleanupIAMUser removes the test user's access keys and user record.
// Uses weed shell since users created via s3.configure must be deleted the same
// way.  Errors are swallowed — AfterEach runs even after test failures.
func cleanupIAMUser(ctx context.Context, username string) {
	// Best-effort IAM API cleanup for any operator-created keys.
	listOut, err := adminIAMClient.ListAccessKeys(ctx, &iam.ListAccessKeysInput{UserName: aws.String(username)})
	if err == nil {
		for _, key := range listOut.AccessKeyMetadata {
			_, _ = adminIAMClient.DeleteAccessKey(ctx, &iam.DeleteAccessKeyInput{
				UserName:    aws.String(username),
				AccessKeyId: key.AccessKeyId,
			})
		}
	}
	// Remove the user from the s3.configure store.
	cmd := fmt.Sprintf("echo 's3.configure -delete -user %s' | weed shell", username)
	_, _, _ = seaweedfsContainer.Exec(ctx, []string{"sh", "-c", cmd})
}
