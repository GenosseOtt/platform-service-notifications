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

// NotificationConfigSpec is the platform-owner configuration for the notifications service.
type NotificationConfigSpec struct {
	// Email configures the email (SMTP) delivery channel. Required while Email is an enabled channel.
	// +optional
	Email *EmailConfig `json:"email,omitempty"`

	// WebAppURL is the base URL of the platform web UI, used to build deep links in
	// notifications (e.g. "https://mycompany.eu"). A trailing slash is tolerated.
	// +optional
	WebAppURL string `json:"webappUrl,omitempty"`

	// ProductName is the platform/product name shown in notification subjects and bodies
	// (e.g. the welcome email greeting). Defaults to "Open Control Plane".
	// +optional
	ProductName string `json:"productName,omitempty"`

	// DocsURL is a link to user-facing documentation, surfaced to new users in the
	// enablement email (e.g. "https://mycompany.eu/help/").
	// +optional
	DocsURL string `json:"docsURL,omitempty"`

	// ConnectURL is a link to documentation on how to connect tools such as kubectl
	// to a control plane. Surfaced as a secondary button in membership-added emails.
	// +optional
	ConnectURL string `json:"connectURL,omitempty"`

	// SupportURL is a link to a support repository or issue tracker where users can
	// report bugs, request features, or ask questions. Shown subtly in all email footers.
	// +optional
	SupportURL string `json:"supportURL,omitempty"`

	// EnabledCategories lists the activity categories that produce notifications.
	// When empty, all categories are enabled.
	// +optional
	EnabledCategories []Category `json:"enabledCategories,omitempty"`

	// EnabledChannels lists the delivery channels that are active.
	// When empty, defaults to [Email].
	// +optional
	EnabledChannels []Channel `json:"enabledChannels,omitempty"`

	// UsernameIsEmail indicates that a User subject's Name can be used directly as an email address.
	// When false, an address must be resolved another way (e.g. UserProfile.spec.email).
	// Defaults to true.
	// +optional
	UsernameIsEmail *bool `json:"usernameIsEmail,omitempty"`

	// NewVersionDigest tunes aggregation for the NewServiceVersion category.
	// +optional
	NewVersionDigest *DigestConfig `json:"newVersionDigest,omitempty"`

	// RecordRetention is how long delivered NotificationRecords are kept before pruning.
	// Defaults to 720h (30 days).
	// +optional
	RecordRetention *metav1.Duration `json:"recordRetention,omitempty"`
}

// EmailConfig holds SMTP delivery settings. Credentials (username/password) live in the
// referenced Secret; connection and sender identity are configured here.
type EmailConfig struct {
	// Host is the SMTP server host.
	Host string `json:"host"`
	// Port is the SMTP server port. Defaults to 587.
	// +optional
	Port int `json:"port,omitempty"`
	// StartTLS requests STARTTLS on the connection. Defaults to true.
	// +optional
	StartTLS *bool `json:"startTLS,omitempty"`
	// SecretRef references a Secret with keys "username" and "password" for SMTP auth.
	// When omitted, the connection is made without authentication.
	// +optional
	SecretRef *LocalSecretReference `json:"secretRef,omitempty"`
	// SenderAddress is the From address.
	SenderAddress string `json:"senderAddress"`
	// SenderName is the display name for the From address.
	// +optional
	SenderName string `json:"senderName,omitempty"`
	// ReplyTo is an optional Reply-To address.
	// +optional
	ReplyTo string `json:"replyTo,omitempty"`
}

// DigestConfig tunes the new-service-version digest aggregation.
type DigestConfig struct {
	// AggregationWindow is how long affected control planes are collected before a single
	// digest is sent per admin. Defaults to 1h.
	// +optional
	AggregationWindow *metav1.Duration `json:"aggregationWindow,omitempty"`
}

// NotificationConfigStatus defines the observed state of NotificationConfig.
type NotificationConfigStatus struct {
	commonapi.Status `json:",inline"`
}

// NotificationConfig is the singleton platform configuration for the notifications service.
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=notifcfg
// +kubebuilder:metadata:labels="openmcp.cloud/cluster=platform"
type NotificationConfig struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// spec defines the desired state of NotificationConfig
	// +required
	Spec NotificationConfigSpec `json:"spec"`

	// status defines the observed state of NotificationConfig
	// +optional
	Status NotificationConfigStatus `json:"status,omitempty,omitzero"`
}

// +kubebuilder:object:root=true

// NotificationConfigList contains a list of NotificationConfig
type NotificationConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NotificationConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(GroupVersion, &NotificationConfig{}, &NotificationConfigList{})
		return nil
	})
}
