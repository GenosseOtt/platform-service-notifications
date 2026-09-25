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

// SubjectKind is the kind of an identity referenced by a membership.
// +kubebuilder:validation:Enum=User;Group;ServiceAccount
type SubjectKind string

const (
	SubjectKindUser           SubjectKind = "User"
	SubjectKindGroup          SubjectKind = "Group"
	SubjectKindServiceAccount SubjectKind = "ServiceAccount"
)

// Subject identifies a platform identity. It mirrors the membership Subject used by
// Projects/Workspaces so recipients can be stored in our own resources without a
// hard dependency on the onboarding API types.
type Subject struct {
	// Kind is the kind of the subject.
	Kind SubjectKind `json:"kind"`
	// Name is the identity string. For Kind=User this is the OIDC username (an email in practice).
	Name string `json:"name"`
	// Namespace is only required for Kind=ServiceAccount.
	// +optional
	Namespace string `json:"namespace,omitempty"`
}

// Category is the type of activity a notification is about.
// +kubebuilder:validation:Enum=MembershipAdded;UserEnablement;NewServiceVersion
type Category string

const (
	// CategoryMembershipAdded: a subject was added to a Project, Workspace, or ControlPlane.
	CategoryMembershipAdded Category = "MembershipAdded"
	// CategoryUserEnablement: a subject was seen on the platform for the first time.
	CategoryUserEnablement Category = "UserEnablement"
	// CategoryNewServiceVersion: a newer version of a service used by a ControlPlane is available.
	CategoryNewServiceVersion Category = "NewServiceVersion"
)

// Channel is a delivery transport for a notification.
// +kubebuilder:validation:Enum=Email;Slack
type Channel string

const (
	ChannelEmail Channel = "Email"
	ChannelSlack Channel = "Slack"
)

// LocalSecretReference references a Secret on the platform cluster.
type LocalSecretReference struct {
	// Name is the name of the Secret.
	Name string `json:"name"`
	// Namespace is the namespace of the Secret. Defaults to the provider's namespace when empty.
	// +optional
	Namespace string `json:"namespace,omitempty"`
}
