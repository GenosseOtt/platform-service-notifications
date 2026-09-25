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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// RecordPhase is the delivery state of a NotificationRecord.
// +kubebuilder:validation:Enum=Pending;Delivered;Failed;Suppressed
type RecordPhase string

const (
	// RecordPhasePending: the notification has been recorded but not yet delivered.
	RecordPhasePending RecordPhase = "Pending"
	// RecordPhaseDelivered: the notification was delivered successfully.
	RecordPhaseDelivered RecordPhase = "Delivered"
	// RecordPhaseFailed: delivery failed after exhausting retries.
	RecordPhaseFailed RecordPhase = "Failed"
	// RecordPhaseSuppressed: delivery was suppressed (e.g. the user opted out).
	RecordPhaseSuppressed RecordPhase = "Suppressed"
)

// NotificationRecordSpec is the immutable description of a single deduplicated notification.
// The object name is the dedup key, so creation is the atomic dedup gate.
type NotificationRecordSpec struct {
	// Category is the activity category.
	Category Category `json:"category"`
	// Channel is the delivery channel this record tracks.
	Channel Channel `json:"channel"`
	// Recipient is the identity the notification is addressed to.
	Recipient Subject `json:"recipient"`
	// RecipientAddress is the resolved delivery address (email address, Slack handle, ...).
	// +optional
	RecipientAddress string `json:"recipientAddress,omitempty"`
	// EventKey is the human-readable identity of the underlying event, e.g.
	// "project/platform-team:member:alice@example.com:admin" or "service/crossplane:v2.3.4".
	EventKey string `json:"eventKey"`
}

// NotificationRecordStatus is the delivery state of a NotificationRecord.
type NotificationRecordStatus struct {
	// Phase is the current delivery phase.
	// +optional
	Phase RecordPhase `json:"phase,omitempty"`
	// Attempts is the number of delivery attempts made.
	// +optional
	Attempts int32 `json:"attempts,omitempty"`
	// LastError is the error from the most recent failed attempt.
	// +optional
	LastError string `json:"lastError,omitempty"`
	// SentAt is when delivery succeeded.
	// +optional
	SentAt *metav1.Time `json:"sentAt,omitempty"`
	// Conditions contains the conditions.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// NotificationRecord is a durable dedup-ledger entry ensuring a given notification is
// delivered to a given recipient at most once.
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=notifrec
// +kubebuilder:printcolumn:name="Category",type=string,JSONPath=`.spec.category`
// +kubebuilder:printcolumn:name="Channel",type=string,JSONPath=`.spec.channel`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:metadata:labels="openmcp.cloud/cluster=platform"
type NotificationRecord struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// spec defines the recorded notification
	// +required
	Spec NotificationRecordSpec `json:"spec"`

	// status defines the delivery state
	// +optional
	Status NotificationRecordStatus `json:"status,omitempty,omitzero"`
}

// +kubebuilder:object:root=true

// NotificationRecordList contains a list of NotificationRecord
type NotificationRecordList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NotificationRecord `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(GroupVersion, &NotificationRecord{}, &NotificationRecordList{})
		return nil
	})
}
