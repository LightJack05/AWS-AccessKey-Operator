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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	awsaccesskeyoperatorv1alpha1 "github.com/LightJack05/AWS-AccessKey-Operator/api/v1alpha1"
)

var _ = Describe("loadAWSConfigFromString", func() {
	var providerConfig *awsaccesskeyoperatorv1alpha1.IAMProviderConfig

	BeforeEach(func() {
		providerConfig = &awsaccesskeyoperatorv1alpha1.IAMProviderConfig{
			Spec: awsaccesskeyoperatorv1alpha1.IAMProviderConfigSpec{
				Region:   "us-east-1",
				Endpoint: "http://localhost:8333",
			},
		}
	})

	It("returns a config with the parsed credentials and the configured region", func() {
		cfg, err := loadAWSConfigFromString(context.Background(), `[default]
aws_access_key_id=AKIAIOSFODNN7EXAMPLE
aws_secret_access_key=wJalrXUtnFEMIK7MDENGbPxRfiCYEXAMPLEKEY`, providerConfig)

		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.Region).To(Equal("us-east-1"))

		creds, err := cfg.Credentials.Retrieve(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(creds.AccessKeyID).To(Equal("AKIAIOSFODNN7EXAMPLE"))
		Expect(creds.SecretAccessKey).To(Equal("wJalrXUtnFEMIK7MDENGbPxRfiCYEXAMPLEKEY"))
	})

	It("returns an error when the [default] section is absent", func() {
		_, err := loadAWSConfigFromString(context.Background(), `[other]
aws_access_key_id=AKIAIOSFODNN7EXAMPLE
aws_secret_access_key=wJalrXUtnFEMIK7MDENGbPxRfiCYEXAMPLEKEY`, providerConfig)

		Expect(err).To(HaveOccurred())
	})

	It("returns an error when aws_access_key_id is absent", func() {
		_, err := loadAWSConfigFromString(context.Background(), `[default]
aws_secret_access_key=wJalrXUtnFEMIK7MDENGbPxRfiCYEXAMPLEKEY`, providerConfig)

		Expect(err).To(HaveOccurred())
	})

	It("returns an error when aws_secret_access_key is absent", func() {
		_, err := loadAWSConfigFromString(context.Background(), `[default]
aws_access_key_id=AKIAIOSFODNN7EXAMPLE`, providerConfig)

		Expect(err).To(HaveOccurred())
	})

	It("returns an error when aws_access_key_id is present but empty", func() {
		_, err := loadAWSConfigFromString(context.Background(), `[default]
aws_access_key_id=
aws_secret_access_key=wJalrXUtnFEMIK7MDENGbPxRfiCYEXAMPLEKEY`, providerConfig)

		Expect(err).To(HaveOccurred())
	})

	It("returns an error when aws_secret_access_key is present but empty", func() {
		_, err := loadAWSConfigFromString(context.Background(), `[default]
aws_access_key_id=AKIAIOSFODNN7EXAMPLE
aws_secret_access_key=`, providerConfig)

		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("isOwnedByAccessKey", func() {
	var (
		accessKey *awsaccesskeyoperatorv1alpha1.IAMAccessKey
		secret    *corev1.Secret
	)

	BeforeEach(func() {
		accessKey = &awsaccesskeyoperatorv1alpha1.IAMAccessKey{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "my-key",
				Namespace: "default",
			},
		}
		secret = &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "my-secret",
				Namespace: "default",
			},
		}
	})

	It("returns true when the secret has a matching owner reference", func() {
		secret.OwnerReferences = []metav1.OwnerReference{
			{
				APIVersion: awsaccesskeyoperatorv1alpha1.GroupVersion.String(),
				Kind:       "IAMAccessKey",
				Name:       "my-key",
			},
		}
		Expect(isOwnedByAccessKey(secret, accessKey)).To(BeTrue())
	})

	It("returns true when a matching ref is present among multiple owner references", func() {
		secret.OwnerReferences = []metav1.OwnerReference{
			{
				APIVersion: "other.group/v1",
				Kind:       "OtherKind",
				Name:       "other-owner",
			},
			{
				APIVersion: awsaccesskeyoperatorv1alpha1.GroupVersion.String(),
				Kind:       "IAMAccessKey",
				Name:       "my-key",
			},
		}
		Expect(isOwnedByAccessKey(secret, accessKey)).To(BeTrue())
	})

	It("returns false when there are no owner references", func() {
		Expect(isOwnedByAccessKey(secret, accessKey)).To(BeFalse())
	})

	It("returns false when the name does not match", func() {
		secret.OwnerReferences = []metav1.OwnerReference{
			{
				APIVersion: awsaccesskeyoperatorv1alpha1.GroupVersion.String(),
				Kind:       "IAMAccessKey",
				Name:       "different-key",
			},
		}
		Expect(isOwnedByAccessKey(secret, accessKey)).To(BeFalse())
	})

	It("returns false when the kind does not match", func() {
		secret.OwnerReferences = []metav1.OwnerReference{
			{
				APIVersion: awsaccesskeyoperatorv1alpha1.GroupVersion.String(),
				Kind:       "SomethingElse",
				Name:       "my-key",
			},
		}
		Expect(isOwnedByAccessKey(secret, accessKey)).To(BeFalse())
	})

	It("returns false when the APIVersion does not match", func() {
		secret.OwnerReferences = []metav1.OwnerReference{
			{
				APIVersion: "other.group/v1",
				Kind:       "IAMAccessKey",
				Name:       "my-key",
			},
		}
		Expect(isOwnedByAccessKey(secret, accessKey)).To(BeFalse())
	})
})
