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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// IAMAdminCredentialsSecretRef references a Secret in the operator namespace
// that contains IAM admin credentials used to manage access keys.
type IAMAdminCredentialsSecretRef struct {
	// name is the name of the Secret within the operator namespace.
	// The Secret must contain the keys "accessKeyId" and "secretAccessKey".
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
}

// IAMProviderConfigSpec defines the desired state of IAMProviderConfig
type IAMProviderConfigSpec struct {
	// endpoint is the base URL of the IAM API endpoint.
	// This can point to AWS IAM (e.g. "https://iam.amazonaws.com") or any
	// compatible implementation such as SeaweedFS
	// (e.g. "http://seaweedfs-filer:8111").
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Pattern=`^https?://`
	Endpoint string `json:"endpoint"`

	// region is the AWS region to use when signing requests.
	// For non-AWS endpoints this value is still required for SigV4 signing,
	// but can be set to an arbitrary string (e.g. "us-east-1").
	// +kubebuilder:validation:MinLength=1
	Region string `json:"region"`

	// adminCredentialsSecretRef references the Secret in the operator namespace
	// that holds the IAM admin credentials used to issue and revoke access keys.
	// +required
	AdminCredentialsSecretRef IAMAdminCredentialsSecretRef `json:"adminCredentialsSecretRef"`
}

// IAMProviderConfigStatus defines the observed state of IAMProviderConfig.
type IAMProviderConfigStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// For Kubernetes API conventions, see:
	// https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md#typical-status-properties

	// conditions represent the current state of the IAMProviderConfig resource.
	// Each condition has a unique type and reflects the status of a specific aspect of the resource.
	//
	// Standard condition types include:
	// - "Available": the resource is fully functional
	// - "Progressing": the resource is being created or updated
	// - "Degraded": the resource failed to reach or maintain its desired state
	//
	// The status of each condition is one of True, False, or Unknown.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// IAMProviderConfig is the Schema for the iamproviderconfigs API
type IAMProviderConfig struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of IAMProviderConfig
	// +required
	Spec IAMProviderConfigSpec `json:"spec"`

	// status defines the observed state of IAMProviderConfig
	// +optional
	Status IAMProviderConfigStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// IAMProviderConfigList contains a list of IAMProviderConfig
type IAMProviderConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []IAMProviderConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&IAMProviderConfig{}, &IAMProviderConfigList{})
}
