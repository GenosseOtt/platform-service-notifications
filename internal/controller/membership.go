package controller

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/openmcp-project/controller-utils/pkg/clusters"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	cpv2alpha1 "github.com/openmcp-project/openmcp-operator/api/core/v2alpha1"
	pwv1alpha1 "github.com/openmcp-project/project-workspace-operator/api/core/v1alpha1"

	"github.com/openmcp-project/platform-service-notifications/api/v1alpha1"
	"github.com/openmcp-project/platform-service-notifications/internal/notify"
	"github.com/openmcp-project/platform-service-notifications/internal/optout"
	"github.com/openmcp-project/platform-service-notifications/internal/store"
)

// member is a normalized membership entry extracted from any watched resource kind.
type member struct {
	subject v1alpha1.Subject
	role    string
}

// membershipHandler holds the shared collaborators for the membership reconcilers. Each
// resource-kind reconciler extracts its members and delegates delivery here.
type membershipHandler struct {
	store    store.Store
	pipeline *notify.Pipeline
}

// process ensures a UserProfile exists for every notifiable member and delivers a
// MembershipAdded notification. Deduplication (the ledger) ensures existing members are not
// re-notified on subsequent reconciles, so we can safely iterate the full member list each time.
func (h *membershipHandler) process(ctx context.Context, kind, namespace, name, displayName string, members []member) error {
	logger := logf.FromContext(ctx)
	var errs []error
	for _, m := range members {
		if !notifiableSubject(m.subject) {
			continue
		}
		// Register the identity; a freshly created profile triggers the enablement flow.
		if _, _, err := h.store.EnsureUserProfile(ctx, m.subject); err != nil {
			errs = append(errs, fmt.Errorf("ensuring user profile for %s: %w", m.subject.Name, err))
			continue
		}

		ev := notify.Event{
			Category:  v1alpha1.CategoryMembershipAdded,
			Recipient: m.subject,
			// Keyed on (resource, subject): the recipient is notified once when first added.
			EventKey: strings.Join([]string{"membership", kind, namespace + "/" + name, subjectID(m.subject)}, ":"),
			Scope:    scopeFor(kind, namespace, name),
			Data: notify.MembershipAddedData{
				ProductName:         h.pipeline.ProductName(),
				RecipientName:       m.subject.Name,
				ResourceKind:        kind,
				ResourceName:        name,
				ResourceDisplayName: displayName,
				Role:                m.role,
				ConsoleURL:          consoleLink(h.pipeline.WebAppURL(), kind, namespace, name),
			},
		}
		if _, err := h.pipeline.Deliver(ctx, ev); err != nil {
			errs = append(errs, fmt.Errorf("delivering membership notification to %s: %w", m.subject.Name, err))
		}
	}
	if len(errs) > 0 {
		logger.V(1).Info("membership processing completed with errors", "count", len(errs))
	}
	return errors.Join(errs...)
}

// primaryRole picks the most privileged role name from a set for display purposes.
func primaryRole(roles []string) string {
	for _, r := range roles {
		if isAdminRole(r) {
			return r
		}
	}
	if len(roles) > 0 {
		return roles[0]
	}
	return ""
}

// scopeFor derives the opt-out Scope for a membership event from the resource kind, its
// namespace (from which project/workspace are recovered), and its name.
func scopeFor(kind, namespace, name string) optout.Scope {
	switch kind {
	case "Project":
		return optout.Scope{Project: name}
	case "Workspace":
		return optout.Scope{Project: projectFromNamespace(namespace), Workspace: name}
	case "ControlPlane":
		project, workspace := projectWorkspaceFromNamespace(namespace)
		return optout.Scope{Project: project, Workspace: workspace, ControlPlane: name}
	}
	return optout.Scope{}
}

// consoleLink builds a deep link into the platform UI (a hash-router SPA) for a resource.
// The UI routes mirror the project → workspace → controlplane hierarchy:
//
//	Project:      <base>/#/projects/<project>
//	Workspace:    <base>/#/projects/<project>/workspaces/<workspace>
//	ControlPlane: <base>/#/projects/<project>/workspaces/<workspace>/controlplane/<cp>
//
// The project/workspace names are recovered from the deterministic openmcp namespace
// convention (project-<p> and project-<p>--ws-<w>). Returns "" when no base URL is
// configured or the hierarchy cannot be determined.
func consoleLink(base, kind, namespace, name string) string {
	if base == "" {
		return ""
	}
	base = strings.TrimRight(base, "/")
	switch kind {
	case "Project":
		return fmt.Sprintf("%s/#/projects/%s", base, name)
	case "Workspace":
		project := projectFromNamespace(namespace)
		if project == "" {
			return ""
		}
		return fmt.Sprintf("%s/#/projects/%s/workspaces/%s", base, project, name)
	case "ControlPlane":
		project, workspace := projectWorkspaceFromNamespace(namespace)
		if project == "" || workspace == "" {
			return ""
		}
		return fmt.Sprintf("%s/#/projects/%s/workspaces/%s/controlplane/%s", base, project, workspace, name)
	default:
		return ""
	}
}

const (
	nsProjectPrefix   = "project-"
	nsWorkspaceMarker = "--ws-"
)

// projectFromNamespace extracts the project name from a workspace namespace "project-<p>".
// It returns "" when the namespace is not a workspace namespace (e.g. a control-plane namespace
// that carries the "--ws-" marker).
func projectFromNamespace(ns string) string {
	if !strings.HasPrefix(ns, nsProjectPrefix) {
		return ""
	}
	rest := strings.TrimPrefix(ns, nsProjectPrefix)
	if rest == "" || strings.Contains(rest, nsWorkspaceMarker) {
		return ""
	}
	return rest
}

// projectWorkspaceFromNamespace extracts (project, workspace) from a control-plane namespace
// "project-<p>--ws-<w>". It returns empty strings when the namespace does not match.
func projectWorkspaceFromNamespace(ns string) (string, string) {
	if !strings.HasPrefix(ns, nsProjectPrefix) {
		return "", ""
	}
	rest := strings.TrimPrefix(ns, nsProjectPrefix)
	i := strings.Index(rest, nsWorkspaceMarker)
	if i <= 0 {
		return "", ""
	}
	project := rest[:i]
	workspace := rest[i+len(nsWorkspaceMarker):]
	if workspace == "" {
		return "", ""
	}
	return project, workspace
}

// --- Project ---

// ProjectMembershipReconciler watches Projects on the onboarding cluster.
type ProjectMembershipReconciler struct {
	membershipHandler
	onboarding   *clusters.Cluster
	providerName string
}

func NewProjectMembershipReconciler(onboarding *clusters.Cluster, s store.Store, p *notify.Pipeline, providerName string) *ProjectMembershipReconciler {
	return &ProjectMembershipReconciler{membershipHandler: membershipHandler{store: s, pipeline: p}, onboarding: onboarding, providerName: providerName}
}

func (r *ProjectMembershipReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	obj := &pwv1alpha1.Project{}
	if err := r.onboarding.Client().Get(ctx, req.NamespacedName, obj); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}
	members := make([]member, 0, len(obj.Spec.Members))
	for _, m := range obj.Spec.Members {
		members = append(members, member{subject: subjectFromPW(m.Subject), role: primaryRole(rolesToStrings(m.Roles))})
	}
	return reconcile.Result{}, r.process(ctx, "Project", obj.Namespace, obj.Name, displayName(obj.Annotations), members)
}

func (r *ProjectMembershipReconciler) SetupWithManager(mgr manager.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&pwv1alpha1.Project{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Named(r.providerName + "-project-membership").
		Complete(r)
}

// --- Workspace ---

// WorkspaceMembershipReconciler watches Workspaces on the onboarding cluster.
type WorkspaceMembershipReconciler struct {
	membershipHandler
	onboarding   *clusters.Cluster
	providerName string
}

func NewWorkspaceMembershipReconciler(onboarding *clusters.Cluster, s store.Store, p *notify.Pipeline, providerName string) *WorkspaceMembershipReconciler {
	return &WorkspaceMembershipReconciler{membershipHandler: membershipHandler{store: s, pipeline: p}, onboarding: onboarding, providerName: providerName}
}

func (r *WorkspaceMembershipReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	obj := &pwv1alpha1.Workspace{}
	if err := r.onboarding.Client().Get(ctx, req.NamespacedName, obj); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}
	members := make([]member, 0, len(obj.Spec.Members))
	for _, m := range obj.Spec.Members {
		members = append(members, member{subject: subjectFromPW(m.Subject), role: primaryRole(rolesToStrings(m.Roles))})
	}
	return reconcile.Result{}, r.process(ctx, "Workspace", obj.Namespace, obj.Name, displayName(obj.Annotations), members)
}

func (r *WorkspaceMembershipReconciler) SetupWithManager(mgr manager.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&pwv1alpha1.Workspace{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Named(r.providerName + "-workspace-membership").
		Complete(r)
}

// --- ControlPlane (V2) ---

// ControlPlaneMembershipReconciler watches V2 ControlPlanes on the onboarding cluster.
type ControlPlaneMembershipReconciler struct {
	membershipHandler
	onboarding   *clusters.Cluster
	providerName string
}

func NewControlPlaneMembershipReconciler(onboarding *clusters.Cluster, s store.Store, p *notify.Pipeline, providerName string) *ControlPlaneMembershipReconciler {
	return &ControlPlaneMembershipReconciler{membershipHandler: membershipHandler{store: s, pipeline: p}, onboarding: onboarding, providerName: providerName}
}

func (r *ControlPlaneMembershipReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	obj := &cpv2alpha1.ControlPlane{}
	if err := r.onboarding.Client().Get(ctx, req.NamespacedName, obj); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}
	members := controlPlaneMembers(obj)
	return reconcile.Result{}, r.process(ctx, "ControlPlane", obj.Namespace, obj.Name, displayName(obj.Annotations), members)
}

func (r *ControlPlaneMembershipReconciler) SetupWithManager(mgr manager.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&cpv2alpha1.ControlPlane{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Named(r.providerName + "-controlplane-membership").
		Complete(r)
}

// controlPlaneMembers flattens a V2 ControlPlane's default-provider role bindings into members,
// deduplicating subjects and keeping their most privileged role.
func controlPlaneMembers(cp *cpv2alpha1.ControlPlane) []member {
	if cp.Spec.IAM.OIDC == nil {
		return nil
	}
	byID := map[string]*member{}
	var order []string
	for _, rb := range cp.Spec.IAM.OIDC.DefaultProvider.RoleBindings {
		roles := make([]string, 0, len(rb.RoleRefs))
		for _, ref := range rb.RoleRefs {
			roles = append(roles, ref.Name)
		}
		role := primaryRole(roles)
		for _, sub := range rb.Subjects {
			s := subjectFromRBAC(sub)
			id := subjectID(s)
			if existing, ok := byID[id]; ok {
				if isAdminRole(role) && !isAdminRole(existing.role) {
					existing.role = role
				}
				continue
			}
			byID[id] = &member{subject: s, role: role}
			order = append(order, id)
		}
	}
	out := make([]member, 0, len(order))
	for _, id := range order {
		out = append(out, *byID[id])
	}
	return out
}

func rolesToStrings[T ~string](roles []T) []string {
	out := make([]string, 0, len(roles))
	for _, r := range roles {
		out = append(out, string(r))
	}
	return out
}

// displayName extracts the human-friendly resource name from annotations, if present.
func displayName(annotations map[string]string) string {
	if annotations == nil {
		return ""
	}
	return annotations[pwv1alpha1.DisplayNameAnnotation]
}
