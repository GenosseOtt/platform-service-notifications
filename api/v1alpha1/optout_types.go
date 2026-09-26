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

// ResourceRef identifies a specific platform resource by kind and name. Used as the target of an
// opt-out object to indicate exactly which resource's notifications should be suppressed.
type ResourceRef struct {
	// Kind is the resource kind: Project, Workspace, or ControlPlane.
	// +kubebuilder:validation:Enum=Project;Workspace;ControlPlane
	Kind string `json:"kind"`
	// Name is the resource name.
	Name string `json:"name"`
}

// NotificationOptOutSpec defines the desired state of a NotificationOptOut.
type NotificationOptOutSpec struct {
	// Target identifies the specific resource whose notifications are muted. When the target
	// kind is higher in the hierarchy than the event's scope, the opt-out cascades down:
	// a Project opt-out suppresses Workspace and ControlPlane events; a Workspace opt-out
	// suppresses ControlPlane events within that workspace.
	Target ResourceRef `json:"target"`

	// Categories lists the notification categories to mute. When empty, all categories are muted.
	// +optional
	Categories []Category `json:"categories,omitempty"`
}

// NotificationOptOut is a resource-wide opt-out managed by project/workspace admins. It mutes
// the targeted resource's notifications for all recipients. For per-user muting see
// UserNotificationOptOut.
//
// The namespace determines the authorization scope: project admins place opt-outs in
// project-<p>; workspace admins place them in project-<p>--ws-<w>. Cascade: a Project-targeted
// opt-out suppresses notifications for all workspaces and control planes underneath.
//
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced,shortName=notifoptout
// +kubebuilder:metadata:labels="openmcp.cloud/cluster=onboarding"
type NotificationOptOut struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// spec defines what to mute
	// +required
	Spec NotificationOptOutSpec `json:"spec"`
}

// +kubebuilder:object:root=true

// NotificationOptOutList contains a list of NotificationOptOut
type NotificationOptOutList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NotificationOptOut `json:"items"`
}

// UserNotificationOptOutSpec defines the desired state of a UserNotificationOptOut.
type UserNotificationOptOutSpec struct {
	// Subject is the user whose notifications are muted. Any project/workspace member may create
	// this object; the "only silence yourself" restriction is a soft convention (no admission
	// webhook is available on the onboarding cluster).
	Subject Subject `json:"subject"`

	// Target identifies the specific resource whose notifications are muted for the subject.
	// Cascade applies: a Project target suppresses Workspace and ControlPlane events for the user.
	Target ResourceRef `json:"target"`

	// Categories lists the notification categories to mute. When empty, all categories are muted.
	// +optional
	Categories []Category `json:"categories,omitempty"`
}

// UserNotificationOptOut is a per-user opt-out creatable by any project or workspace member.
// It mutes notifications for the named subject on the targeted resource. For resource-wide
// muting (affecting all recipients) see NotificationOptOut.
//
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced,shortName=usernotifoptout
// +kubebuilder:metadata:labels="openmcp.cloud/cluster=onboarding"
type UserNotificationOptOut struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// spec defines whom to mute and on what resource
	// +required
	Spec UserNotificationOptOutSpec `json:"spec"`
}

// +kubebuilder:object:root=true

// UserNotificationOptOutList contains a list of UserNotificationOptOut
type UserNotificationOptOutList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []UserNotificationOptOut `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(GroupVersion,
			&NotificationOptOut{}, &NotificationOptOutList{},
			&UserNotificationOptOut{}, &UserNotificationOptOutList{},
		)
		return nil
	})
}
