/*
Copyright 2022.

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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// UsageSpec defines the desired state of Usage
type UsageSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// +optional
	Nodes corev1.NodeSelectorTerm `json:"NodeSelector,omitempty"`
	// Selector *metav1.LabelSelector `json:"selector,omitempty" protobuf:"bytes,2,name=selector"`

	// metrics contains the specifications for which to use to calculate the
	// desired replica count (the maximum replica count across all metrics will
	// be used).  The desired replica count is calculated multiplying the
	// ratio between the target value and the current value by the current
	// number of pods.  Ergo, metrics used must decrease as the pod count is
	// increased, and vice-versa.  See the individual metric source types for
	// more information about how each type of metric must respond.
	// If not set, the default metric will be set to 80% average CPU utilization.
	// +listType=atomic
	// +optional
	Metrics []MetricSpec `json:"metrics,omitempty" protobuf:"bytes,4,rep,name=metrics"`
}

// UsageStatus defines the observed state of Usage
type UsageStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
	NodeCount int `json:"NodeCount,omitempty"`
}

// MetricSpec specifies how to scale based on a single metric
// (only `type` and one other matching field should be set at once).
type MetricSpec struct {
	// type is the type of metric source.  It should be one of "ContainerResource", "External",
	// "Object", "Pods" or "Resource", each mapping to a matching field in the object.
	// Note: "ContainerResource" type is available on when the feature-gate
	// HPAContainerMetrics is enabled
	Type MetricSourceType `json:"type" protobuf:"bytes,1,name=type"`
	// name is the name of the given metric
	Name string `json:"name" protobuf:"bytes,1,name=name"`
}

// MetricSourceType indicates the type of metric.
type MetricSourceType string

const (
	// PodsMetricSourceType is a metric describing each pod in the current scale
	// target (for example, transactions-processed-per-second).  The values
	// will be averaged together before being compared to the target value.
	NodesMetricSourceType MetricSourceType = "Node"
)

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:resource:scope=Cluster

// Usage is the Schema for the usages API
type Usage struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   UsageSpec   `json:"spec,omitempty"`
	Status UsageStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// UsageList contains a list of Usage
type UsageList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Usage `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Usage{}, &UsageList{})
}
