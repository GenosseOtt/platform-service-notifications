package controller

import (
	"testing"

	rbacv1 "k8s.io/api/rbac/v1"

	commonapi "github.com/openmcp-project/openmcp-operator/api/common"
	cpv2alpha1 "github.com/openmcp-project/openmcp-operator/api/core/v2alpha1"

	"github.com/openmcp-project/platform-service-notifications/api/v1alpha1"
)

const (
	roleAdmin       = "admin"
	roleView        = "view"
	kindClusterRole = "ClusterRole"
	testBaseURL     = "https://mycompany.eu"
)

func TestSubjectKind(t *testing.T) {
	cases := map[string]v1alpha1.SubjectKind{
		"User":           v1alpha1.SubjectKindUser,
		"Group":          v1alpha1.SubjectKindGroup,
		"ServiceAccount": v1alpha1.SubjectKindServiceAccount,
		"":               v1alpha1.SubjectKindUser, // defaults to User
		"Weird":          v1alpha1.SubjectKindUser,
	}
	for in, want := range cases {
		if got := subjectKind(in); got != want {
			t.Errorf("subjectKind(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSubjectID(t *testing.T) {
	s := v1alpha1.Subject{Kind: v1alpha1.SubjectKindUser, Namespace: "ns", Name: "a@x.io"}
	if got, want := subjectID(s), "User/ns/a@x.io"; got != want {
		t.Errorf("subjectID = %q, want %q", got, want)
	}
}

func TestNotifiableSubject(t *testing.T) {
	if !notifiableSubject(v1alpha1.Subject{Kind: v1alpha1.SubjectKindUser, Name: "a@x.io"}) {
		t.Error("a named User should be notifiable")
	}
	if notifiableSubject(v1alpha1.Subject{Kind: v1alpha1.SubjectKindUser, Name: ""}) {
		t.Error("an unnamed User should not be notifiable")
	}
	if notifiableSubject(v1alpha1.Subject{Kind: v1alpha1.SubjectKindGroup, Name: "grp"}) {
		t.Error("a Group should not be notifiable")
	}
}

func TestIsAdminRole(t *testing.T) {
	admin := []string{roleAdmin, "Admin", "cluster-admin", "project:admin", "CLUSTER-ADMIN"}
	notAdmin := []string{roleView, "viewer", "edit", "", "administrator-ish"}
	for _, r := range admin {
		if !isAdminRole(r) {
			t.Errorf("isAdminRole(%q) = false, want true", r)
		}
	}
	for _, r := range notAdmin {
		if isAdminRole(r) {
			t.Errorf("isAdminRole(%q) = true, want false", r)
		}
	}
}

func TestPrimaryRole(t *testing.T) {
	if got := primaryRole([]string{roleView, roleAdmin}); got != roleAdmin {
		t.Errorf("primaryRole should prefer admin, got %q", got)
	}
	if got := primaryRole([]string{roleView, "edit"}); got != roleView {
		t.Errorf("primaryRole with no admin should return first, got %q", got)
	}
	if got := primaryRole(nil); got != "" {
		t.Errorf("primaryRole(nil) should be empty, got %q", got)
	}
}

func TestConsoleLink(t *testing.T) {
	cases := []struct {
		name              string
		base, kind        string
		namespace, object string
		want              string
	}{
		{"empty base", "", "Project", "", "p", ""},
		{"project", testBaseURL + "/", "Project", "", "poc-demo-world", testBaseURL + "/#/projects/poc-demo-world"},
		{"workspace", testBaseURL, "Workspace", "project-argo-meets-co", "intro-session", testBaseURL + "/#/projects/argo-meets-co/workspaces/intro-session"},
		{"controlplane", testBaseURL, "ControlPlane", "project-argo-meets-co--ws-intro-session", "democp", testBaseURL + "/#/projects/argo-meets-co/workspaces/intro-session/controlplane/democp"},
		{"workspace bad ns", testBaseURL, "Workspace", "not-a-project-ns", "w", ""},
		{"controlplane bad ns", testBaseURL, "ControlPlane", "project-p", "cp", ""},
		{"unknown kind", testBaseURL, "Widget", "ns", "n", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := consoleLink(tc.base, tc.kind, tc.namespace, tc.object); got != tc.want {
				t.Errorf("consoleLink = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestProjectWorkspaceFromNamespace(t *testing.T) {
	if p := projectFromNamespace("project-poc"); p != "poc" {
		t.Errorf("projectFromNamespace = %q, want poc", p)
	}
	if p := projectFromNamespace("project-poc--ws-w"); p != "" {
		t.Errorf("projectFromNamespace on a CP namespace should be empty, got %q", p)
	}
	if p := projectFromNamespace("other"); p != "" {
		t.Errorf("projectFromNamespace on non-project ns should be empty, got %q", p)
	}
	p, w := projectWorkspaceFromNamespace("project-argo--ws-intro")
	if p != "argo" || w != "intro" {
		t.Errorf("projectWorkspaceFromNamespace = (%q,%q), want (argo,intro)", p, w)
	}
	if p, w := projectWorkspaceFromNamespace("project-p"); p != "" || w != "" {
		t.Errorf("projectWorkspaceFromNamespace without marker should be empty, got (%q,%q)", p, w)
	}
}

func TestParseServiceGVK(t *testing.T) {
	cases := []struct {
		kind       string
		apiVersion string
		wantGroup  string
		wantVer    string
		wantErr    bool
	}{
		{"MyKind", "mygroup.io/v1", "mygroup.io", "v1", false},
		{"MyKind", "v1", "", "v1", false},
		{"MyKind", "services.example.io/v1beta1", "services.example.io", "v1beta1", false},
		{"MyKind", "", "", "", true},
	}
	for _, tc := range cases {
		gvk, err := parseServiceGVK(tc.kind, tc.apiVersion)
		if tc.wantErr {
			if err == nil {
				t.Errorf("parseServiceGVK(%q, %q): expected error, got none", tc.kind, tc.apiVersion)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseServiceGVK(%q, %q): unexpected error: %v", tc.kind, tc.apiVersion, err)
			continue
		}
		if gvk.Group != tc.wantGroup || gvk.Version != tc.wantVer || gvk.Kind != tc.kind {
			t.Errorf("parseServiceGVK(%q, %q) = %v, want group=%q ver=%q kind=%q",
				tc.kind, tc.apiVersion, gvk, tc.wantGroup, tc.wantVer, tc.kind)
		}
	}
}

func cpWithBindings(rbs ...commonapi.RoleBindings) *cpv2alpha1.ControlPlane {
	return &cpv2alpha1.ControlPlane{
		Spec: cpv2alpha1.ControlPlaneSpec{
			IAM: cpv2alpha1.IAMConfig{
				OIDC: &cpv2alpha1.OIDCConfig{
					DefaultProvider: cpv2alpha1.DefaultProviderConfig{RoleBindings: rbs},
				},
			},
		},
	}
}

func user(name string) rbacv1.Subject { return rbacv1.Subject{Kind: "User", Name: name} }

func TestControlPlaneAdmins(t *testing.T) {
	cp := cpWithBindings(
		commonapi.RoleBindings{
			Subjects: []rbacv1.Subject{user("admin1@x.io"), user("admin2@x.io")},
			RoleRefs: []commonapi.RoleRef{{Name: roleAdmin, Kind: kindClusterRole}},
		},
		commonapi.RoleBindings{
			Subjects: []rbacv1.Subject{user("viewer@x.io")},
			RoleRefs: []commonapi.RoleRef{{Name: roleView, Kind: kindClusterRole}},
		},
		commonapi.RoleBindings{ // duplicate admin1 across bindings must dedup
			Subjects: []rbacv1.Subject{user("admin1@x.io")},
			RoleRefs: []commonapi.RoleRef{{Name: "cluster-admin", Kind: kindClusterRole}},
		},
	)
	admins := controlPlaneAdmins(cp)
	got := map[string]bool{}
	for _, a := range admins {
		got[a.Name] = true
	}
	if len(admins) != 2 || !got["admin1@x.io"] || !got["admin2@x.io"] {
		t.Fatalf("expected exactly admin1,admin2, got %+v", admins)
	}
	if got["viewer@x.io"] {
		t.Error("viewer must not be treated as admin")
	}
}

func TestControlPlaneAdmins_NoOIDC(t *testing.T) {
	cp := &cpv2alpha1.ControlPlane{}
	if admins := controlPlaneAdmins(cp); admins != nil {
		t.Errorf("expected nil admins when OIDC unset, got %+v", admins)
	}
}

func TestControlPlaneMembers(t *testing.T) {
	cp := cpWithBindings(
		commonapi.RoleBindings{
			Subjects: []rbacv1.Subject{user("u@x.io")},
			RoleRefs: []commonapi.RoleRef{{Name: roleView, Kind: kindClusterRole}},
		},
		commonapi.RoleBindings{ // same subject later gains admin -> role should upgrade
			Subjects: []rbacv1.Subject{user("u@x.io")},
			RoleRefs: []commonapi.RoleRef{{Name: roleAdmin, Kind: kindClusterRole}},
		},
	)
	members := controlPlaneMembers(cp)
	if len(members) != 1 {
		t.Fatalf("expected 1 deduped member, got %d: %+v", len(members), members)
	}
	if members[0].role != roleAdmin {
		t.Errorf("expected role to upgrade to admin, got %q", members[0].role)
	}
}

func TestRolesToStrings(t *testing.T) {
	type roleT string
	got := rolesToStrings([]roleT{"admin", "view"})
	if len(got) != 2 || got[0] != "admin" || got[1] != "view" {
		t.Errorf("rolesToStrings = %+v", got)
	}
}
