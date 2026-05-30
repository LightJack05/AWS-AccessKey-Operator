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

// IAMProviderGrantSpec defines the desired state of IAMProviderGrant.
type IAMProviderGrantSpec struct {
	// providerConfigRef references the IAMProviderConfig that IAMAccessKey resources
	// in this namespace are permitted to use.
	// +required
	ProviderConfigRef IAMProviderConfigRef `json:"providerConfigRef"`

	// allowedUsernames is the list of IAM usernames that may be requested via
	// IAMAccessKey resources in this namespace for the referenced provider.
	// +kubebuilder:validation:MinItems=1
	// +required
	AllowedUsernames []string `json:"allowedUsernames"`
}

// +kubebuilder:object:root=true

// IAMProviderGrant is the Schema for the iamprovidergrants API.
// A cluster admin places an IAMProviderGrant in a namespace to allow
// IAMAccessKey resources in that namespace to use the referenced
// IAMProviderConfig and to restrict which IAM usernames they may request.
type IAMProviderGrant struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of IAMProviderGrant
	// +required
	Spec IAMProviderGrantSpec `json:"spec"`
}

// +kubebuilder:object:root=true

// IAMProviderGrantList contains a list of IAMProviderGrant
type IAMProviderGrantList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []IAMProviderGrant `json:"items"`
}

func init() {
	SchemeBuilder.Register(&IAMProviderGrant{}, &IAMProviderGrantList{})
}
