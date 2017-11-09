package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const BuildResourcePlural = "builds"

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type Build struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`
	Spec              BuildSpec   `json:"spec"`
	Status            BuildStatus `json:"status,omitempty"`
}

type BuildSpec struct {
	Foo string `json:"foo"`
	Bar bool   `json:"bar"`
}

type BuildStatus struct {
	State   BuildState `json:"state,omitempty"`
	Message string     `json:"message,omitempty"`
}

type BuildState string

const (
	BuildStateCreated   BuildState = "Created"
	BuildStateProcessed BuildState = "Processed"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type BuildList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`
	Items           []Build `json:"items"`
}
