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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// SchedulerSpec defines the desired state of Scheduler.
type SchedulerSpec struct {
	// Sched specifies the URI of the OCI scheduler artifact
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Pattern=`^(([a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?)(\.[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?)*(:[0-9]+)?/)?[a-z0-9]+((\.|_|__|-+)[a-z0-9]+)*(/[a-z0-9]+((\.|_|__|-+)[a-z0-9]+)*)*(:[a-zA-Z0-9_][a-zA-Z0-9._-]{0,127})?(@[a-zA-Z][a-zA-Z0-9]*([._+-][a-zA-Z0-9]+)*:[a-fA-F0-9]{32,})?$`
	// +kubebuilder:validation:XValidation:rule="!self.contains('@') || self.matches('^.*@sha256:[a-fA-F0-9]{64}$')",message="spec.sched must pin a sha256 digest of 64 hex characters"
	Sched string `json:"sched"`

	// NodeSelector limits the scheduler workload to nodes carrying every
	// listed label. An empty selector targets every node the tolerations allow.
	// +optional
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`

	// Tolerations allow the scheduler workload to run on tainted nodes.
	// +optional
	Tolerations []corev1.Toleration `json:"tolerations,omitempty"`

	// Args are the arguments passed to the scheduler container.
	// +optional
	Args []string `json:"args,omitempty"`

	// Env is the environment exposed to the scheduler container.
	// +optional
	Env []corev1.EnvVar `json:"env,omitempty"`

	// ImagePullSecrets are the secrets used to pull the scheduler image.
	// +optional
	ImagePullSecrets []corev1.LocalObjectReference `json:"imagePullSecrets,omitempty"`

	// Resources describes the compute resources reserved for the scheduler container.
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`
}

const (
	SchedulerConditionActive      = "Active"
	SchedulerConditionReady       = "Ready"
	SchedulerConditionProgressing = "Progressing"
	SchedulerConditionDegraded    = "Degraded"
)

const (
	ReasonReconcileSucceeded      = "ReconcileSucceeded"
	ReasonRolloutInProgress       = "RolloutInProgress"
	ReasonDaemonSetReady          = "DaemonSetReady"
	ReasonNoNodesScheduled        = "NoNodesScheduled"
	ReasonNodesUnavailable        = "NodesUnavailable"
	ReasonSpecInvalid             = "SpecInvalid"
	ReasonImageVerificationFailed = "ImageVerificationFailed"
	ReasonReconcileFailed         = "ReconcileFailed"
	ReasonActiveScheduler         = "ActiveScheduler"
	ReasonSchedulerConflict       = "SchedulerConflict"
)

type SchedulerNodeStatus struct {
	Desired     int32 `json:"desired"`
	Ready       int32 `json:"ready"`
	Available   int32 `json:"available"`
	Unavailable int32 `json:"unavailable"`
}

// SchedulerStatus defines the observed state of Scheduler.
type SchedulerStatus struct {
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// ResolvedImage is the digest-pinned reference of the verified scheduler image
	// that the controller scheduled. It lets operators audit the exact content
	// that is running.
	ResolvedImage string `json:"resolvedImage,omitempty"`

	Nodes SchedulerNodeStatus `json:"nodes"`

	// +listType=map
	// +listMapKey=type
	// +patchStrategy=merge
	// +patchMergeKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}

// Scheduler installs a sched_ext scheduler on the nodes it targets.
//
// It is cluster-scoped because it changes node-level kernel behavior, and a node
// runs at most one scheduler. Several Schedulers may exist at once as long as
// their node selections do not overlap.
//
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="Desired",type="integer",JSONPath=".status.nodes.desired"
// +kubebuilder:printcolumn:name="Available",type="integer",JSONPath=".status.nodes.available"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
type Scheduler struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SchedulerSpec   `json:"spec,omitempty"`
	Status SchedulerStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// SchedulerList contains a list of Scheduler.
type SchedulerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Scheduler `json:"items"`
}
