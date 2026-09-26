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
	commonapi "github.com/openmcp-project/openmcp-operator/api/common"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// UserProfileSpec defines the desired state of a platform user's notification profile.
// It doubles as the platform user registry (one per distinct identity), the email
// resolution source, and the opt-out store.
type UserProfileSpec struct {
	// Subject is the identity this profile belongs to.
	Subject Subject `json:"subject"`

	// Email overrides the address used for email delivery. When empty, the address is
	// resolved from the subject (see NotificationConfig.spec.usernameIsEmail).
	// +optional
	Email string `json:"email,omitempty"`

	// Preferences captures the user's opt-out and channel choices.
	// +optional
	Preferences NotificationPreferences `json:"preferences,omitempty"`
}

// NotificationPreferences captures per-user channel selection.
// Opt-out is now managed via NotificationOptOut / UserNotificationOptOut on the onboarding
// cluster, where users have direct API access.
type NotificationPreferences struct {
	// Channels selects preferred delivery channels. When empty, the config default is used.
	// +optional
	Channels []Channel `json:"channels,omitempty"`
}

// UserProfileStatus defines the observed state of UserProfile.
type UserProfileStatus struct {
	commonapi.Status `json:",inline"`

	// FirstSeen is when this identity was first observed on the platform.
	// +optional
	FirstSeen *metav1.Time `json:"firstSeen,omitempty"`

	// ResolvedEmail is the address the controller resolved for this user.
	// +optional
	ResolvedEmail string `json:"resolvedEmail,omitempty"`

	// EnablementSentAt records when the one-time enablement email was sent.
	// +optional
	EnablementSentAt *metav1.Time `json:"enablementSentAt,omitempty"`
}

// UserProfile is the Schema for the UserProfile API.
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=uprof
// +kubebuilder:printcolumn:name="Subject",type=string,JSONPath=`.spec.subject.name`
// +kubebuilder:printcolumn:name="Email",type=string,JSONPath=`.status.resolvedEmail`
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:metadata:labels="openmcp.cloud/cluster=platform"
type UserProfile struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// spec defines the desired state of UserProfile
	// +required
	Spec UserProfileSpec `json:"spec"`

	// status defines the observed state of UserProfile
	// +optional
	Status UserProfileStatus `json:"status,omitempty,omitzero"`
}

// +kubebuilder:object:root=true

// UserProfileList contains a list of UserProfile
type UserProfileList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []UserProfile `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(GroupVersion, &UserProfile{}, &UserProfileList{})
		return nil
	})
}
