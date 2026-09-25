package controller

import (
	"fmt"
	"strings"

	rbacv1 "k8s.io/api/rbac/v1"

	cpv2alpha1 "github.com/openmcp-project/openmcp-operator/api/core/v2alpha1"
	pwv1alpha1 "github.com/openmcp-project/project-workspace-operator/api/core/v1alpha1"

	"github.com/openmcp-project/platform-service-notifications/api/v1alpha1"
)

// subjectKind maps an external subject kind string onto our enum, defaulting to User.
func subjectKind(k string) v1alpha1.SubjectKind {
	switch k {
	case string(v1alpha1.SubjectKindGroup):
		return v1alpha1.SubjectKindGroup
	case string(v1alpha1.SubjectKindServiceAccount):
		return v1alpha1.SubjectKindServiceAccount
	default:
		return v1alpha1.SubjectKindUser
	}
}

// subjectFromPW converts a Project/Workspace membership subject to our Subject.
func subjectFromPW(s pwv1alpha1.Subject) v1alpha1.Subject {
	return v1alpha1.Subject{Kind: subjectKind(s.Kind), Name: s.Name, Namespace: s.Namespace}
}

// subjectFromRBAC converts an rbac.Subject (used by V2 ControlPlane role bindings) to our Subject.
func subjectFromRBAC(s rbacv1.Subject) v1alpha1.Subject {
	return v1alpha1.Subject{Kind: subjectKind(s.Kind), Name: s.Name, Namespace: s.Namespace}
}

// subjectID is a stable identity string for a subject, used inside event keys.
func subjectID(s v1alpha1.Subject) string {
	return fmt.Sprintf("%s/%s/%s", s.Kind, s.Namespace, s.Name)
}

// notifiableSubject reports whether we can address a subject at all. Only User subjects can be
// emailed today; Groups and ServiceAccounts have no resolvable address.
func notifiableSubject(s v1alpha1.Subject) bool {
	return s.Kind == v1alpha1.SubjectKindUser && s.Name != ""
}

// isAdminRole reports whether a Project/Workspace/ControlPlane role name denotes an admin.
func isAdminRole(role string) bool {
	r := strings.ToLower(role)
	return r == "admin" || r == "cluster-admin" || strings.HasSuffix(r, ":admin")
}

// controlPlaneAdmins returns the distinct admin subjects of a V2 ControlPlane, derived from the
// default OIDC provider's role bindings whose roleRefs include an admin role.
func controlPlaneAdmins(cp *cpv2alpha1.ControlPlane) []v1alpha1.Subject {
	if cp.Spec.IAM.OIDC == nil {
		return nil
	}
	var out []v1alpha1.Subject
	seen := map[string]struct{}{}
	for _, rb := range cp.Spec.IAM.OIDC.DefaultProvider.RoleBindings {
		admin := false
		for _, ref := range rb.RoleRefs {
			if isAdminRole(ref.Name) {
				admin = true
				break
			}
		}
		if !admin {
			continue
		}
		for _, sub := range rb.Subjects {
			s := subjectFromRBAC(sub)
			if !notifiableSubject(s) {
				continue
			}
			id := subjectID(s)
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			out = append(out, s)
		}
	}
	return out
}
