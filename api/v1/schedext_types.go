/*
Copyright 2025.

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

package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// SchedExtSpec defines the desired state of SchedExt.
type SchedExtSpec struct {
	// Sched specifies the URI of the OCI scheduler artifact
	Sched string `json:"sched"`
}

// SchedExtStatus defines the observed state of SchedExt.
type SchedExtStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// SchedExt is the Schema for the schedexts API.
type SchedExt struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SchedExtSpec   `json:"spec,omitempty"`
	Status SchedExtStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// SchedExtList contains a list of SchedExt.
type SchedExtList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SchedExt `json:"items"`
}

func init() {
	SchemeBuilder.Register(&SchedExt{}, &SchedExtList{})
}
