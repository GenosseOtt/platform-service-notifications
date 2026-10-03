package controller

import (
	"context"
	"testing"
	"time"

	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/openmcp-project/controller-utils/pkg/clusters"
	commonapi "github.com/openmcp-project/openmcp-operator/api/common"
	cpv2alpha1 "github.com/openmcp-project/openmcp-operator/api/core/v2alpha1"

	"github.com/openmcp-project/platform-service-notifications/api/v1alpha1"
	"github.com/openmcp-project/platform-service-notifications/internal/notify"
	"github.com/openmcp-project/platform-service-notifications/internal/optout"
	"github.com/openmcp-project/platform-service-notifications/internal/store"
)

// ─── schemes ────────────────────────────────────────────────────────────────

var digestOnboardingScheme = func() *runtime.Scheme {
	s := runtime.NewScheme()
	if err := cpv2alpha1.AddToScheme(s); err != nil {
		panic(err)
	}
	return s
}()

// ─── test doubles ────────────────────────────────────────────────────────────

// digestStore is a minimal store.Store for reconciler tests.
// All Claims succeed by default; set terminalForAll=true to simulate dedup.
type digestStore struct {
	claimed        []store.Notification
	delivered      int
	suppressed     int
	terminalForAll bool
}

func (s *digestStore) Claim(_ context.Context, n store.Notification) (bool, *v1alpha1.NotificationRecord, error) {
	s.claimed = append(s.claimed, n)
	if s.terminalForAll {
		return false, &v1alpha1.NotificationRecord{}, nil
	}
	return true, &v1alpha1.NotificationRecord{}, nil
}
func (s *digestStore) MarkDelivered(_ context.Context, _ *v1alpha1.NotificationRecord) error {
	s.delivered++
	return nil
}
func (s *digestStore) MarkFailed(_ context.Context, _ *v1alpha1.NotificationRecord, _ error) error {
	return nil
}
func (s *digestStore) MarkSuppressed(_ context.Context, _ *v1alpha1.NotificationRecord, _ string) error {
	s.suppressed++
	return nil
}
func (s *digestStore) EnsureUserProfile(_ context.Context, _ v1alpha1.Subject) (*v1alpha1.UserProfile, bool, error) {
	return nil, false, nil
}
func (s *digestStore) GetUserProfile(_ context.Context, _ v1alpha1.Subject) (*v1alpha1.UserProfile, error) {
	return nil, nil // username-is-email mode: Subject.Name is the email address
}
func (s *digestStore) SetEnablementSent(_ context.Context, _ *v1alpha1.UserProfile, _ time.Time) error {
	return nil
}
func (s *digestStore) PruneExpired(_ context.Context, _ time.Duration) (int, error) { return 0, nil }

// digestNotifier captures the Message.To from every Send call.
type digestNotifier struct {
	recipients []string
}

func (n *digestNotifier) Channel() v1alpha1.Channel { return v1alpha1.ChannelEmail }
func (n *digestNotifier) Send(_ context.Context, m notify.Message) error {
	n.recipients = append(n.recipients, m.To)
	return nil
}

// digestRenderer captures the raw data argument so tests can inspect AffectedControlPlanes
// without parsing rendered HTML.
type digestRenderer struct {
	calls []any
}

func (r *digestRenderer) Render(_ v1alpha1.Category, data any) (notify.Rendered, error) {
	r.calls = append(r.calls, data)
	return notify.Rendered{Subject: "version-digest", HTML: "html", Text: "text"}, nil
}

// digestSuppressor suppresses delivery for specific (admin, CP) pairs.
// Key format: "admin@example.com/cp-name".
type digestSuppressor struct {
	blocked map[string]bool
}

func (f *digestSuppressor) Suppressed(_ context.Context, recipient v1alpha1.Subject, _ v1alpha1.Category, scope optout.Scope) (bool, string, error) {
	if f.blocked[recipient.Name+"/"+scope.ControlPlane] {
		return true, "test opt-out", nil
	}
	return false, "", nil
}

// ─── builder helpers ─────────────────────────────────────────────────────────

// digestCP creates a ControlPlane with all given addresses as admins.
//
//nolint:unparam // ns always testCPNS in current tests but the helper is deliberately general
func digestCP(name, ns string, adminEmails ...string) *cpv2alpha1.ControlPlane {
	subjects := make([]rbacv1.Subject, 0, len(adminEmails))
	for _, e := range adminEmails {
		subjects = append(subjects, user(e))
	}
	rb := commonapi.RoleBindings{
		Subjects: subjects,
		RoleRefs: []commonapi.RoleRef{{Name: roleAdmin, Kind: kindClusterRole}},
	}
	cp := cpWithBindings(rb)
	cp.Name = name
	cp.Namespace = ns
	return cp
}

// digestMS creates an unstructured ManagedService with a single service entry using the given GVK
// and versions. The object is named "catalog" (cluster-scoped).
func digestMS(serviceName string, gvk metav1.GroupVersionKind, versions ...string) *unstructured.Unstructured {
	versionsList := make([]interface{}, 0, len(versions))
	for _, v := range versions {
		versionsList = append(versionsList, map[string]interface{}{"version": v})
	}
	apiVersion := gvk.Version
	if gvk.Group != "" {
		apiVersion = gvk.Group + "/" + gvk.Version
	}
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(managedServiceGVK)
	obj.SetName("catalog")
	_ = unstructured.SetNestedSlice(obj.Object, []interface{}{
		map[string]interface{}{
			"name":       serviceName,
			"kind":       gvk.Kind,
			"apiVersion": apiVersion,
			"versions":   versionsList,
		},
	}, "spec", "services")
	return obj
}

// digestSvcInst creates an unstructured service instance whose (name, ns) matches its ControlPlane.
//
//nolint:unparam // ns always testCPNS in current tests but the helper is deliberately general
func digestSvcInst(group, version, kind, name, ns string) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(schema.GroupVersionKind{Group: group, Version: version, Kind: kind})
	obj.SetName(name)
	obj.SetNamespace(ns)
	return obj
}

// buildDigestReconciler wires up all fakes and returns a VersionDigestReconciler ready to Reconcile.
func buildDigestReconciler(
	ms *unstructured.Unstructured,
	cps []*cpv2alpha1.ControlPlane,
	svcInsts []*unstructured.Unstructured,
	st *digestStore,
	n *digestNotifier,
	r *digestRenderer,
	sup optout.Suppressor,
) *VersionDigestReconciler {
	// Onboarding cluster: knows ManagedService, ControlPlanes, and unstructured service instances.
	onboardingObjs := make([]client.Object, 0, 1+len(cps)+len(svcInsts))
	onboardingObjs = append(onboardingObjs, ms)
	for _, cp := range cps {
		onboardingObjs = append(onboardingObjs, cp)
	}
	for _, inst := range svcInsts {
		onboardingObjs = append(onboardingObjs, inst)
	}
	onboardingFake := fake.NewClientBuilder().
		WithScheme(digestOnboardingScheme).
		WithObjects(onboardingObjs...).
		Build()
	onboardingCluster := clusters.NewTestClusterFromClient("onboarding", onboardingFake)

	pipeline := notify.NewPipeline(
		st, r,
		notify.Settings{
			EnabledChannels: []v1alpha1.Channel{v1alpha1.ChannelEmail},
			UsernameIsEmail: true,
			WebAppURL:       testBaseURL,
			ProductName:     "Test Platform",
		},
		nil, // pipeline suppressor is a no-op; opt-out handled in the reconciler itself
		n,
	)

	return NewVersionDigestReconciler(onboardingCluster, st, pipeline, sup, "test-provider")
}

// ─── tests ───────────────────────────────────────────────────────────────────

// svcGVK is the GVK used for service instances across tests.
var svcGVK = metav1.GroupVersionKind{Group: "services.test", Version: "v1", Kind: "TestService"}

// testCPNS is a workspace namespace so projectWorkspaceFromNamespace resolves to a valid pair.
const testCPNS = "project-poc--ws-dev"

func reconcileDigest(t *testing.T, rec *VersionDigestReconciler, name string) {
	t.Helper()
	_, err := rec.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: client.ObjectKey{Name: name},
	})
	if err != nil {
		t.Fatalf("Reconcile returned unexpected error: %v", err)
	}
}

// TestVersionDigestReconciler_OneAdmin_OneDigest verifies that a single admin with two
// affected ControlPlanes receives exactly one email containing both CPs.
func TestVersionDigestReconciler_OneAdmin_OneDigest(t *testing.T) {
	cp1 := digestCP("cp-alpha", testCPNS, "alice@x.io")
	cp2 := digestCP("cp-beta", testCPNS, "alice@x.io")
	inst1 := digestSvcInst(svcGVK.Group, svcGVK.Version, svcGVK.Kind, "cp-alpha", testCPNS)
	inst2 := digestSvcInst(svcGVK.Group, svcGVK.Version, svcGVK.Kind, "cp-beta", testCPNS)

	st := &digestStore{}
	n := &digestNotifier{}
	r := &digestRenderer{}

	ms := digestMS("crossplane", svcGVK, "v1.2.1")
	rec := buildDigestReconciler(ms, []*cpv2alpha1.ControlPlane{cp1, cp2}, []*unstructured.Unstructured{inst1, inst2}, st, n, r, nil)
	reconcileDigest(t, rec, "catalog")

	if len(n.recipients) != 1 {
		t.Fatalf("expected 1 email sent, got %d: %v", len(n.recipients), n.recipients)
	}
	if n.recipients[0] != "alice@x.io" {
		t.Errorf("expected email to alice@x.io, got %q", n.recipients[0])
	}

	data := r.calls[0].(notify.NewVersionDigestData)
	if len(data.AffectedControlPlanes) != 2 {
		t.Errorf("expected 2 affected CPs in digest, got %d: %+v", len(data.AffectedControlPlanes), data.AffectedControlPlanes)
	}
	if data.NewVersion != "v1.2.1" {
		t.Errorf("expected NewVersion=v1.2.1, got %q", data.NewVersion)
	}
	if data.ServiceName != "crossplane" {
		t.Errorf("expected ServiceName=%q, got %q", "crossplane", data.ServiceName)
	}
}

// TestVersionDigestReconciler_TwoAdmins_SeparateDigests verifies that two distinct admins
// each receive their own independent digest.
func TestVersionDigestReconciler_TwoAdmins_SeparateDigests(t *testing.T) {
	cp1 := digestCP("cp-alice", testCPNS, "alice@x.io") // alice only
	cp2 := digestCP("cp-bob", testCPNS, "bob@x.io")     // bob only
	inst1 := digestSvcInst(svcGVK.Group, svcGVK.Version, svcGVK.Kind, "cp-alice", testCPNS)
	inst2 := digestSvcInst(svcGVK.Group, svcGVK.Version, svcGVK.Kind, "cp-bob", testCPNS)

	st := &digestStore{}
	n := &digestNotifier{}
	r := &digestRenderer{}

	ms := digestMS("svc", svcGVK, "v2")
	rec := buildDigestReconciler(ms, []*cpv2alpha1.ControlPlane{cp1, cp2}, []*unstructured.Unstructured{inst1, inst2}, st, n, r, nil)
	reconcileDigest(t, rec, "catalog")

	if len(n.recipients) != 2 {
		t.Fatalf("expected 2 emails (one per admin), got %d: %v", len(n.recipients), n.recipients)
	}
	got := map[string]bool{}
	for _, rcpt := range n.recipients {
		got[rcpt] = true
	}
	if !got["alice@x.io"] || !got["bob@x.io"] {
		t.Errorf("expected alice and bob each to receive a digest, got %v", got)
	}
	// Each admin's digest should contain only their own CP.
	for _, call := range r.calls {
		data := call.(notify.NewVersionDigestData)
		if len(data.AffectedControlPlanes) != 1 {
			t.Errorf("each admin should have exactly 1 CP in digest, got %d: %+v", len(data.AffectedControlPlanes), data.AffectedControlPlanes)
		}
	}
}

// TestVersionDigestReconciler_SharedAdmin_AllCPsInOneDigest verifies that an admin who appears
// on multiple ControlPlanes receives a single digest listing all their CPs.
func TestVersionDigestReconciler_SharedAdmin_AllCPsInOneDigest(t *testing.T) {
	// alice is on both CPs; bob is only on cp2.
	cp1 := digestCP("cp-one", testCPNS, "alice@x.io")
	cp2 := digestCP("cp-two", testCPNS, "alice@x.io", "bob@x.io")
	inst1 := digestSvcInst(svcGVK.Group, svcGVK.Version, svcGVK.Kind, "cp-one", testCPNS)
	inst2 := digestSvcInst(svcGVK.Group, svcGVK.Version, svcGVK.Kind, "cp-two", testCPNS)

	st := &digestStore{}
	n := &digestNotifier{}
	r := &digestRenderer{}

	ms := digestMS("svc", svcGVK, "v3")
	rec := buildDigestReconciler(ms, []*cpv2alpha1.ControlPlane{cp1, cp2}, []*unstructured.Unstructured{inst1, inst2}, st, n, r, nil)
	reconcileDigest(t, rec, "catalog")

	// alice and bob each get exactly 1 email.
	if len(n.recipients) != 2 {
		t.Fatalf("expected 2 emails (alice + bob), got %d: %v", len(n.recipients), n.recipients)
	}

	// alice's digest must contain both CPs; bob's only one.
	for _, call := range r.calls {
		data := call.(notify.NewVersionDigestData)
		switch data.RecipientName {
		case "alice@x.io":
			if len(data.AffectedControlPlanes) != 2 {
				t.Errorf("alice should have 2 CPs in digest, got %d: %+v", len(data.AffectedControlPlanes), data.AffectedControlPlanes)
			}
		case "bob@x.io":
			if len(data.AffectedControlPlanes) != 1 {
				t.Errorf("bob should have 1 CP in digest, got %d: %+v", len(data.AffectedControlPlanes), data.AffectedControlPlanes)
			}
		default:
			t.Errorf("unexpected recipient: %q", data.RecipientName)
		}
	}
}

// TestVersionDigestReconciler_NoVersions_NoDelivery verifies that a ManagedService with a service
// entry that has no versions listed produces no notifications.
func TestVersionDigestReconciler_NoVersions_NoDelivery(t *testing.T) {
	cp := digestCP("cp", testCPNS, "alice@x.io")
	inst := digestSvcInst(svcGVK.Group, svcGVK.Version, svcGVK.Kind, "cp", testCPNS)

	// digestMS with no version arguments → empty versions list
	ms := digestMS("svc", svcGVK)

	st := &digestStore{}
	n := &digestNotifier{}
	r := &digestRenderer{}

	rec := buildDigestReconciler(ms, []*cpv2alpha1.ControlPlane{cp}, []*unstructured.Unstructured{inst}, st, n, r, nil)
	reconcileDigest(t, rec, "catalog")

	if len(n.recipients) != 0 {
		t.Errorf("no delivery expected for service with no versions, got %d sends: %v", len(n.recipients), n.recipients)
	}
}

// TestVersionDigestReconciler_NoServices_NoDelivery verifies that a ManagedService with an empty
// spec.services list sends no notifications.
func TestVersionDigestReconciler_NoServices_NoDelivery(t *testing.T) {
	// ManagedService with no services at all.
	ms := &unstructured.Unstructured{}
	ms.SetGroupVersionKind(managedServiceGVK)
	ms.SetName("catalog")

	onboardingFake := fake.NewClientBuilder().
		WithScheme(digestOnboardingScheme).
		WithObjects(ms).
		Build()
	onboardingCluster := clusters.NewTestClusterFromClient("onboarding", onboardingFake)

	st := &digestStore{}
	n := &digestNotifier{}
	r := &digestRenderer{}
	pipeline := notify.NewPipeline(st, r, notify.Settings{EnabledChannels: []v1alpha1.Channel{v1alpha1.ChannelEmail}, UsernameIsEmail: true}, nil, n)

	rec := NewVersionDigestReconciler(onboardingCluster, st, pipeline, nil, "test")
	reconcileDigest(t, rec, "catalog")

	if len(n.recipients) != 0 {
		t.Errorf("no delivery expected for empty spec.services, got %d sends", len(n.recipients))
	}
}

// TestVersionDigestReconciler_OptOut_OneCPExcluded verifies that when an admin opts out of one
// CP, that CP is removed from their digest but the admin still receives the remaining CPs.
func TestVersionDigestReconciler_OptOut_OneCPExcluded(t *testing.T) {
	cp1 := digestCP("cp-keep", testCPNS, "alice@x.io")
	cp2 := digestCP("cp-muted", testCPNS, "alice@x.io")
	inst1 := digestSvcInst(svcGVK.Group, svcGVK.Version, svcGVK.Kind, "cp-keep", testCPNS)
	inst2 := digestSvcInst(svcGVK.Group, svcGVK.Version, svcGVK.Kind, "cp-muted", testCPNS)

	st := &digestStore{}
	n := &digestNotifier{}
	r := &digestRenderer{}
	// alice opted out of "cp-muted" only.
	sup := &digestSuppressor{blocked: map[string]bool{"alice@x.io/cp-muted": true}}

	ms := digestMS("svc", svcGVK, "v4")
	rec := buildDigestReconciler(ms, []*cpv2alpha1.ControlPlane{cp1, cp2}, []*unstructured.Unstructured{inst1, inst2}, st, n, r, sup)
	reconcileDigest(t, rec, "catalog")

	if len(n.recipients) != 1 {
		t.Fatalf("expected alice to still receive a digest, got %d sends: %v", len(n.recipients), n.recipients)
	}
	data := r.calls[0].(notify.NewVersionDigestData)
	if len(data.AffectedControlPlanes) != 1 {
		t.Errorf("alice's digest should list only the non-muted CP, got %d: %+v", len(data.AffectedControlPlanes), data.AffectedControlPlanes)
	}
	if data.AffectedControlPlanes[0].Name != "cp-keep" {
		t.Errorf("expected only cp-keep in digest, got %q", data.AffectedControlPlanes[0].Name)
	}
}

// TestVersionDigestReconciler_AllOptedOut_NoDelivery verifies that when all CPs are opted out
// for an admin, they receive no email at all.
func TestVersionDigestReconciler_AllOptedOut_NoDelivery(t *testing.T) {
	cp := digestCP("cp-muted", testCPNS, "alice@x.io")
	inst := digestSvcInst(svcGVK.Group, svcGVK.Version, svcGVK.Kind, "cp-muted", testCPNS)

	st := &digestStore{}
	n := &digestNotifier{}
	r := &digestRenderer{}
	sup := &digestSuppressor{blocked: map[string]bool{"alice@x.io/cp-muted": true}}

	ms := digestMS("svc", svcGVK, "v5")
	rec := buildDigestReconciler(ms, []*cpv2alpha1.ControlPlane{cp}, []*unstructured.Unstructured{inst}, st, n, r, sup)
	reconcileDigest(t, rec, "catalog")

	if len(n.recipients) != 0 {
		t.Errorf("expected no email when all CPs are opted out, got %d sends: %v", len(n.recipients), n.recipients)
	}
}

// TestVersionDigestReconciler_ViewersNotNotified verifies that only admins (not viewers) are
// included in a version digest.
func TestVersionDigestReconciler_ViewersNotNotified(t *testing.T) {
	cp := &cpv2alpha1.ControlPlane{}
	cp.Name = "cp-mixed"
	cp.Namespace = testCPNS
	cp.Spec.IAM.OIDC = &cpv2alpha1.OIDCConfig{
		DefaultProvider: cpv2alpha1.DefaultProviderConfig{
			RoleBindings: []commonapi.RoleBindings{
				{
					Subjects: []rbacv1.Subject{user("admin@x.io")},
					RoleRefs: []commonapi.RoleRef{{Name: roleAdmin, Kind: kindClusterRole}},
				},
				{
					Subjects: []rbacv1.Subject{user("viewer@x.io")},
					RoleRefs: []commonapi.RoleRef{{Name: roleView, Kind: kindClusterRole}},
				},
			},
		},
	}
	inst := digestSvcInst(svcGVK.Group, svcGVK.Version, svcGVK.Kind, "cp-mixed", testCPNS)

	st := &digestStore{}
	n := &digestNotifier{}
	r := &digestRenderer{}

	ms := digestMS("svc", svcGVK, "v6")
	rec := buildDigestReconciler(ms, []*cpv2alpha1.ControlPlane{cp}, []*unstructured.Unstructured{inst}, st, n, r, nil)
	reconcileDigest(t, rec, "catalog")

	if len(n.recipients) != 1 {
		t.Fatalf("expected exactly 1 recipient (admin only), got %d: %v", len(n.recipients), n.recipients)
	}
	if n.recipients[0] != "admin@x.io" {
		t.Errorf("expected admin@x.io, got %q", n.recipients[0])
	}
}

// TestVersionDigestReconciler_EventKeyIncludesServiceAndVersion verifies the dedup event key
// format ("newversion:<service>:<version>") so that version changes produce new events.
func TestVersionDigestReconciler_EventKeyIncludesServiceAndVersion(t *testing.T) {
	cp := digestCP("cp", testCPNS, "alice@x.io")
	inst := digestSvcInst(svcGVK.Group, svcGVK.Version, svcGVK.Kind, "cp", testCPNS)

	st := &digestStore{}
	n := &digestNotifier{}
	r := &digestRenderer{}

	ms := digestMS("crossplane", svcGVK, "v1.14.0")
	rec := buildDigestReconciler(ms, []*cpv2alpha1.ControlPlane{cp}, []*unstructured.Unstructured{inst}, st, n, r, nil)
	reconcileDigest(t, rec, "catalog")

	if len(st.claimed) == 0 {
		t.Fatal("expected at least one claimed notification")
	}
	wantKey := "newversion:crossplane:v1.14.0"
	if st.claimed[0].EventKey != wantKey {
		t.Errorf("expected EventKey=%q, got %q", wantKey, st.claimed[0].EventKey)
	}
}

// TestVersionDigestReconciler_MultipleVersions_OneNotifPerVersion verifies that each version
// listed in the catalog produces an independent notification (dedup key includes the version).
func TestVersionDigestReconciler_MultipleVersions_OneNotifPerVersion(t *testing.T) {
	cp := digestCP("cp", testCPNS, "alice@x.io")
	inst := digestSvcInst(svcGVK.Group, svcGVK.Version, svcGVK.Kind, "cp", testCPNS)

	st := &digestStore{}
	n := &digestNotifier{}
	r := &digestRenderer{}

	ms := digestMS("crossplane", svcGVK, "v1.13.0", "v1.14.0")
	rec := buildDigestReconciler(ms, []*cpv2alpha1.ControlPlane{cp}, []*unstructured.Unstructured{inst}, st, n, r, nil)
	reconcileDigest(t, rec, "catalog")

	// Two versions → two claims, two emails.
	if len(st.claimed) != 2 {
		t.Fatalf("expected 2 claimed notifications (one per version), got %d", len(st.claimed))
	}
	keys := map[string]bool{}
	for _, c := range st.claimed {
		keys[c.EventKey] = true
	}
	if !keys["newversion:crossplane:v1.13.0"] || !keys["newversion:crossplane:v1.14.0"] {
		t.Errorf("expected keys for both versions, got %v", keys)
	}
}
