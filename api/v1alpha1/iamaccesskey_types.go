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

type IAMAccessKeyConditions string

const (
	ConditionReady = "Ready"
)

// IAMProviderConfigRef references an IAMProviderConfig by name and namespace.
type IAMProviderConfigRef struct {
	// name is the name of the IAMProviderConfig.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// namespace is the namespace of the IAMProviderConfig.
	// +kubebuilder:validation:MinLength=1
	Namespace string `json:"namespace"`
}

// IAMAccessKeySpec defines the desired state of IAMAccessKey
type IAMAccessKeySpec struct {
	// providerConfigRef references the IAMProviderConfig that defines the IAM
	// endpoint and credentials to use when managing this access key.
	// +required
	ProviderConfigRef IAMProviderConfigRef `json:"providerConfigRef"`

	// username is the IAM username this access key belongs to.
	// +kubebuilder:validation:MinLength=1
	// +required
	Username string `json:"username"`

	// secretName is the name of the Kubernetes Secret where the access key ID and secret access key will be stored after creation.
	// The Secret will be created in the same namespace as the IAMAccessKey resource.
	// +kubebuilder:validation:MinLength=1
	// +required
	SecretName string `json:"secretName"`

	// secretKey is the key within the Kubernetes Secret where the access key ID and secret access key will be stored in standard AWS INI format
	// +kubebuilder:validation:MinLength=1
	// +required
	SecretKey string `json:"secretField"`
}

// IAMAccessKeyStatus defines the observed state of IAMAccessKey.
type IAMAccessKeyStatus struct {
	// message provides a human-readable explanation of the current status,
	// such as the reason the access key is unhealthy or any relevant error.
	// +optional
	Message string `json:"message,omitempty"`

	// conditions represent the current state of the IAMAccessKey resource.
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
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// IAMAccessKey is the Schema for the iamaccesskeys API
type IAMAccessKey struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of IAMAccessKey
	// +required
	Spec IAMAccessKeySpec `json:"spec"`

	// status defines the observed state of IAMAccessKey
	// +optional
	Status IAMAccessKeyStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// IAMAccessKeyList contains a list of IAMAccessKey
type IAMAccessKeyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []IAMAccessKey `json:"items"`
}

func init() {
	SchemeBuilder.Register(&IAMAccessKey{}, &IAMAccessKeyList{})
}
