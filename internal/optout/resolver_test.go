package optout_test

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/openmcp-project/platform-service-notifications/api/v1alpha1"
	"github.com/openmcp-project/platform-service-notifications/internal/optout"
)

const (
	kindProject      = "Project"
	kindWorkspace    = "Workspace"
	kindControlPlane = "ControlPlane"

	testProject   = "poc"
	testWorkspace = "dev"
	testCPName    = "prod"
)

// scheme registers our v1alpha1 types so the fake client can handle them.
var scheme = func() *runtime.Scheme {
	s := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(s); err != nil {
		panic(err)
	}
	return s
}()

// helpers

func userSubject(name string) v1alpha1.Subject {
	return v1alpha1.Subject{Kind: v1alpha1.SubjectKindUser, Name: name}
}

func projectScope(p string) optout.Scope { return optout.Scope{Project: p} }

// workspaceScope builds a workspace-level opt-out Scope. p is the project, ws the workspace.
//
//nolint:unparam // p always "poc" in current tests but function is deliberately general
func workspaceScope(p, ws string) optout.Scope {
	return optout.Scope{Project: p, Workspace: ws}
}
func cpScope(p, ws, cp string) optout.Scope {
	return optout.Scope{Project: p, Workspace: ws, ControlPlane: cp}
}

func makeOptOut(name, ns string, target v1alpha1.ResourceRef, cats ...v1alpha1.Category) *v1alpha1.NotificationOptOut {
	return &v1alpha1.NotificationOptOut{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec:       v1alpha1.NotificationOptOutSpec{Target: target, Categories: cats},
	}
}

// makeUserOptOut creates a UserNotificationOptOut for testing.
//
//nolint:unparam // name always "alice-mute" in current tests but function is deliberately general
func makeUserOptOut(name, ns string, subject v1alpha1.Subject, target v1alpha1.ResourceRef, cats ...v1alpha1.Category) *v1alpha1.UserNotificationOptOut {
	return &v1alpha1.UserNotificationOptOut{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: v1alpha1.UserNotificationOptOutSpec{
			Subject:    subject,
			Target:     target,
			Categories: cats,
		},
	}
}

func newResolver(objs ...client.Object) *optout.Resolver {
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
	return optout.New(c)
}

// Tests

func TestResolver_EmptyScope_NotSuppressed(t *testing.T) {
	// Empty scope (enablement email, pre-filtered digest) must never be suppressed.
	r := newResolver()
	sup, reason, err := r.Suppressed(context.Background(), userSubject("alice"), v1alpha1.CategoryUserEnablement, optout.Scope{})
	if err != nil || sup || reason != "" {
		t.Fatalf("empty scope: got suppressed=%v reason=%q err=%v", sup, reason, err)
	}
}

func TestResolver_NoOptOuts_NotSuppressed(t *testing.T) {
	r := newResolver()
	sup, _, err := r.Suppressed(context.Background(), userSubject("alice"), v1alpha1.CategoryMembershipAdded, cpScope("myproject", "maws", "prod-cp"))
	if err != nil || sup {
		t.Fatalf("expected not suppressed, got suppressed=%v err=%v", sup, err)
	}
}

// --- NotificationOptOut cascade tests ---

func TestResolver_ProjectOptOut_SuppressesProjectEvent(t *testing.T) {
	obj := makeOptOut("mute-all", "project-poc", v1alpha1.ResourceRef{Kind: kindProject, Name: testProject})
	r := newResolver(obj)

	sup, reason, err := r.Suppressed(context.Background(), userSubject("alice"), v1alpha1.CategoryMembershipAdded, projectScope(testProject))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !sup {
		t.Fatalf("expected suppressed, got false")
	}
	if reason == "" {
		t.Fatalf("expected non-empty reason")
	}
}

func TestResolver_ProjectOptOut_CascadesToWorkspaceEvent(t *testing.T) {
	// A project-level opt-out should suppress workspace-scope events in the same project.
	obj := makeOptOut("mute-all", "project-poc", v1alpha1.ResourceRef{Kind: kindProject, Name: testProject})
	r := newResolver(obj)

	sup, _, err := r.Suppressed(context.Background(), userSubject("alice"), v1alpha1.CategoryMembershipAdded, workspaceScope(testProject, testWorkspace))
	if err != nil || !sup {
		t.Fatalf("expected project opt-out to cascade to workspace event: suppressed=%v err=%v", sup, err)
	}
}

func TestResolver_ProjectOptOut_CascadesToCPEvent(t *testing.T) {
	// A project-level opt-out should suppress CP-scope events in the same project.
	obj := makeOptOut("mute-all", "project-poc", v1alpha1.ResourceRef{Kind: kindProject, Name: testProject})
	r := newResolver(obj)

	sup, _, err := r.Suppressed(context.Background(), userSubject("alice"), v1alpha1.CategoryNewServiceVersion, cpScope(testProject, testWorkspace, "cp-1"))
	if err != nil || !sup {
		t.Fatalf("expected project opt-out to cascade to CP event: suppressed=%v err=%v", sup, err)
	}
}

func TestResolver_ProjectOptOut_DoesNotAffectOtherProject(t *testing.T) {
	obj := makeOptOut("mute-all", "project-poc", v1alpha1.ResourceRef{Kind: kindProject, Name: testProject})
	r := newResolver(obj)

	sup, _, err := r.Suppressed(context.Background(), userSubject("alice"), v1alpha1.CategoryMembershipAdded, projectScope("other-project"))
	if err != nil || sup {
		t.Fatalf("opt-out in project-poc must not affect other-project: suppressed=%v err=%v", sup, err)
	}
}

func TestResolver_WorkspaceOptOut_SuppressesWorkspaceEvent(t *testing.T) {
	// Workspace opt-out in the project namespace suppresses events for that workspace.
	obj := makeOptOut("mute-ws", "project-poc", v1alpha1.ResourceRef{Kind: kindWorkspace, Name: testWorkspace})
	r := newResolver(obj)

	sup, _, err := r.Suppressed(context.Background(), userSubject("alice"), v1alpha1.CategoryMembershipAdded, workspaceScope(testProject, testWorkspace))
	if err != nil || !sup {
		t.Fatalf("expected workspace opt-out to suppress workspace event: suppressed=%v err=%v", sup, err)
	}
}

func TestResolver_WorkspaceOptOut_CascadesToCPEvent(t *testing.T) {
	// A workspace-level opt-out should cascade to CP events in that workspace.
	obj := makeOptOut("mute-ws", "project-poc", v1alpha1.ResourceRef{Kind: kindWorkspace, Name: testWorkspace})
	r := newResolver(obj)

	sup, _, err := r.Suppressed(context.Background(), userSubject("alice"), v1alpha1.CategoryNewServiceVersion, cpScope(testProject, testWorkspace, "cp-1"))
	if err != nil || !sup {
		t.Fatalf("expected workspace opt-out to cascade to CP event: suppressed=%v err=%v", sup, err)
	}
}

func TestResolver_WorkspaceOptOut_DoesNotAffectSiblingWorkspace(t *testing.T) {
	// A workspace opt-out for "dev" must not suppress events in "staging".
	obj := makeOptOut("mute-ws", "project-poc", v1alpha1.ResourceRef{Kind: kindWorkspace, Name: testWorkspace})
	r := newResolver(obj)

	sup, _, err := r.Suppressed(context.Background(), userSubject("alice"), v1alpha1.CategoryMembershipAdded, workspaceScope(testProject, "staging"))
	if err != nil || sup {
		t.Fatalf("opt-out for 'dev' must not affect 'staging': suppressed=%v err=%v", sup, err)
	}
}

func TestResolver_CPOptOut_SuppressesCPEvent(t *testing.T) {
	// A CP-level opt-out placed in the workspace namespace suppresses that specific CP.
	obj := makeOptOut("mute-cp", "project-poc--ws-dev", v1alpha1.ResourceRef{Kind: kindControlPlane, Name: testCPName})
	r := newResolver(obj)

	sup, _, err := r.Suppressed(context.Background(), userSubject("alice"), v1alpha1.CategoryNewServiceVersion, cpScope(testProject, testWorkspace, testCPName))
	if err != nil || !sup {
		t.Fatalf("expected CP opt-out to suppress CP event: suppressed=%v err=%v", sup, err)
	}
}

func TestResolver_CPOptOut_DoesNotAffectSiblingCP(t *testing.T) {
	// A CP opt-out for "prod" must not suppress events for "staging" in the same workspace.
	obj := makeOptOut("mute-cp", "project-poc--ws-dev", v1alpha1.ResourceRef{Kind: kindControlPlane, Name: testCPName})
	r := newResolver(obj)

	sup, _, err := r.Suppressed(context.Background(), userSubject("alice"), v1alpha1.CategoryNewServiceVersion, cpScope(testProject, testWorkspace, "staging"))
	if err != nil || sup {
		t.Fatalf("opt-out for 'prod' must not affect 'staging': suppressed=%v err=%v", sup, err)
	}
}

func TestResolver_CPOptOut_DoesNotCascadeUp(t *testing.T) {
	// A CP-level opt-out must NOT suppress project- or workspace-scope events.
	obj := makeOptOut("mute-cp", "project-poc--ws-dev", v1alpha1.ResourceRef{Kind: kindControlPlane, Name: testCPName})
	r := newResolver(obj)

	// Workspace-scope event must not be affected.
	sup, _, err := r.Suppressed(context.Background(), userSubject("alice"), v1alpha1.CategoryMembershipAdded, workspaceScope(testProject, testWorkspace))
	if err != nil || sup {
		t.Fatalf("CP opt-out must not cascade up to workspace event: suppressed=%v err=%v", sup, err)
	}
}

// --- Category subset tests ---

func TestResolver_CategorySubset_Match(t *testing.T) {
	obj := makeOptOut("mute-cat", "project-poc",
		v1alpha1.ResourceRef{Kind: kindProject, Name: testProject},
		v1alpha1.CategoryNewServiceVersion)
	r := newResolver(obj)

	sup, _, err := r.Suppressed(context.Background(), userSubject("alice"), v1alpha1.CategoryNewServiceVersion, projectScope(testProject))
	if err != nil || !sup {
		t.Fatalf("expected suppressed for matching category: suppressed=%v err=%v", sup, err)
	}
}

func TestResolver_CategorySubset_NoMatch(t *testing.T) {
	// Opt-out specifies NewServiceVersion; event is MembershipAdded → not suppressed.
	obj := makeOptOut("mute-cat", "project-poc",
		v1alpha1.ResourceRef{Kind: kindProject, Name: testProject},
		v1alpha1.CategoryNewServiceVersion)
	r := newResolver(obj)

	sup, _, err := r.Suppressed(context.Background(), userSubject("alice"), v1alpha1.CategoryMembershipAdded, projectScope(testProject))
	if err != nil || sup {
		t.Fatalf("opt-out for NewServiceVersion must not suppress MembershipAdded: suppressed=%v err=%v", sup, err)
	}
}

func TestResolver_EmptyCategories_SuppressesAll(t *testing.T) {
	// No categories = mute all.
	obj := makeOptOut("mute-all", "project-poc", v1alpha1.ResourceRef{Kind: kindProject, Name: testProject})
	r := newResolver(obj)

	for _, cat := range []v1alpha1.Category{v1alpha1.CategoryMembershipAdded, v1alpha1.CategoryNewServiceVersion, v1alpha1.CategoryUserEnablement} {
		sup, _, err := r.Suppressed(context.Background(), userSubject("alice"), cat, projectScope(testProject))
		if err != nil || !sup {
			t.Fatalf("empty-categories opt-out must suppress %q: suppressed=%v err=%v", cat, sup, err)
		}
	}
}

// --- UserNotificationOptOut tests ---

func TestResolver_UserOptOut_SubjectMatch(t *testing.T) {
	alice := userSubject("alice")
	obj := makeUserOptOut("alice-mute", "project-poc", alice, v1alpha1.ResourceRef{Kind: kindProject, Name: testProject})
	r := newResolver(obj)

	// Alice is suppressed.
	sup, _, err := r.Suppressed(context.Background(), alice, v1alpha1.CategoryMembershipAdded, projectScope(testProject))
	if err != nil || !sup {
		t.Fatalf("alice's opt-out should suppress alice: suppressed=%v err=%v", sup, err)
	}

	// Bob is not suppressed.
	sup, _, err = r.Suppressed(context.Background(), userSubject("bob"), v1alpha1.CategoryMembershipAdded, projectScope(testProject))
	if err != nil || sup {
		t.Fatalf("alice's opt-out must not suppress bob: suppressed=%v err=%v", sup, err)
	}
}

func TestResolver_UserOptOut_WorkspaceNamespace(t *testing.T) {
	// A user opt-out placed in the workspace namespace is found when the event scope includes that workspace.
	alice := userSubject("alice")
	obj := makeUserOptOut("alice-mute", "project-poc--ws-dev", alice, v1alpha1.ResourceRef{Kind: kindWorkspace, Name: testWorkspace})
	r := newResolver(obj)

	sup, _, err := r.Suppressed(context.Background(), alice, v1alpha1.CategoryMembershipAdded, workspaceScope(testProject, testWorkspace))
	if err != nil || !sup {
		t.Fatalf("user opt-out in ws namespace should be found: suppressed=%v err=%v", sup, err)
	}
}

func TestResolver_UserOptOut_CategorySubset(t *testing.T) {
	alice := userSubject("alice")
	obj := makeUserOptOut("alice-mute", "project-poc", alice,
		v1alpha1.ResourceRef{Kind: kindProject, Name: testProject},
		v1alpha1.CategoryNewServiceVersion)
	r := newResolver(obj)

	// NewServiceVersion is suppressed for alice.
	sup, _, err := r.Suppressed(context.Background(), alice, v1alpha1.CategoryNewServiceVersion, projectScope(testProject))
	if err != nil || !sup {
		t.Fatalf("expected suppressed for matching category: suppressed=%v err=%v", sup, err)
	}

	// MembershipAdded is NOT suppressed.
	sup, _, err = r.Suppressed(context.Background(), alice, v1alpha1.CategoryMembershipAdded, projectScope(testProject))
	if err != nil || sup {
		t.Fatalf("user opt-out category must not suppress other categories: suppressed=%v err=%v", sup, err)
	}
}

func TestResolver_UserOptOut_CascadeToCP(t *testing.T) {
	// A project-level user opt-out should cascade to CP-scope events.
	alice := userSubject("alice")
	obj := makeUserOptOut("alice-mute", "project-poc", alice, v1alpha1.ResourceRef{Kind: kindProject, Name: testProject})
	r := newResolver(obj)

	sup, _, err := r.Suppressed(context.Background(), alice, v1alpha1.CategoryNewServiceVersion, cpScope(testProject, testWorkspace, "cp-1"))
	if err != nil || !sup {
		t.Fatalf("user project-opt-out should cascade to CP events: suppressed=%v err=%v", sup, err)
	}
}

// --- Workspace isolation test ---

func TestResolver_WorkspaceIsolation_OptOutInOneWorkspaceDoesNotAffectOther(t *testing.T) {
	// An opt-out placed in project-poc--ws-dev should not suppress events in project-poc--ws-staging.
	obj := makeOptOut("mute-all", "project-poc--ws-dev", v1alpha1.ResourceRef{Kind: kindWorkspace, Name: testWorkspace})
	r := newResolver(obj)

	sup, _, err := r.Suppressed(context.Background(), userSubject("alice"), v1alpha1.CategoryMembershipAdded, workspaceScope(testProject, "staging"))
	if err != nil || sup {
		t.Fatalf("opt-out in ws/dev namespace must not affect staging workspace: suppressed=%v err=%v", sup, err)
	}
}
