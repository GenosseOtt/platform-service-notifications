/*
Package optout provides the suppression resolver for the notifications service. It checks whether
a notification should be suppressed based on opt-out objects stored on the onboarding cluster
(NotificationOptOut and UserNotificationOptOut CRDs).

The resolver is deliberately separate from internal/store (which is platform-cluster-backed) and
uses an onboarding-cluster cache-backed client — the same one the membership reconcilers hold.
*/
package optout

import (
	"context"
	"fmt"
	"strings"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openmcp-project/platform-service-notifications/api/v1alpha1"
)

// Scope describes the resource context of a notification event. Fields are hierarchical:
// a ControlPlane event has all three set; a Workspace event has Project+Workspace; a Project
// event has only Project.
type Scope struct {
	Project      string
	Workspace    string
	ControlPlane string
}

// Suppressor evaluates whether a given notification should be suppressed based on opt-out
// resources on the onboarding cluster.
type Suppressor interface {
	// Suppressed returns (true, reason, nil) when the event should be silenced. A non-nil error
	// means the check itself failed (e.g. the informer cache is not yet synced) and the caller
	// should requeue. A nil error with false means "deliver normally".
	Suppressed(ctx context.Context, recipient v1alpha1.Subject, category v1alpha1.Category, scope Scope) (bool, string, error)
}

// NoOpSuppressor never suppresses. Used as the default when no onboarding client is available
// (e.g. in unit tests that only test delivery logic) and for UserEnablement events that carry
// an empty scope.
type NoOpSuppressor struct{}

func (NoOpSuppressor) Suppressed(_ context.Context, _ v1alpha1.Subject, _ v1alpha1.Category, _ Scope) (bool, string, error) {
	return false, "", nil
}

// Resolver evaluates opt-out resources on the onboarding cluster. It uses the cache-backed
// client so each call is a local cache read, not a live API request.
//
// Important: the resolver is only valid while the manager (built on the onboarding cluster) is
// running, because that is what keeps the informer cache synced. If the manager ever moves to
// the platform cluster, the client here needs its own onboarding informer.
type Resolver struct {
	c client.Client
}

// New returns a Resolver backed by the provided onboarding-cluster client.
func New(c client.Client) *Resolver {
	return &Resolver{c: c}
}

// Suppressed checks whether any opt-out resource in the candidate namespaces for the given scope
// matches this event. The empty-scope fast-path returns false immediately (used for UserEnablement
// and pre-filtered NewServiceVersion events).
func (r *Resolver) Suppressed(ctx context.Context, recipient v1alpha1.Subject, category v1alpha1.Category, scope Scope) (bool, string, error) {
	if scope.Project == "" {
		// No onboarding scope: enablement email or fully pre-filtered aggregate. Never suppressed
		// here; governed only by config EnabledCategories.
		return false, "", nil
	}

	namespaces := candidateNamespaces(scope)

	for _, ns := range namespaces {
		// Check resource-wide opt-outs (admin-managed, any recipient).
		var list v1alpha1.NotificationOptOutList
		if err := r.c.List(ctx, &list, client.InNamespace(ns)); err != nil {
			return false, "", fmt.Errorf("listing NotificationOptOut in %s: %w", ns, err)
		}
		for i := range list.Items {
			item := &list.Items[i]
			if targetMatches(item.Spec.Target, scope) && categoryMatches(item.Spec.Categories, category) {
				return true, fmt.Sprintf("resource opt-out %q in %s targets %s/%s", item.Name, ns, item.Spec.Target.Kind, item.Spec.Target.Name), nil
			}
		}

		// Check per-user opt-outs (self-service, any member).
		var userList v1alpha1.UserNotificationOptOutList
		if err := r.c.List(ctx, &userList, client.InNamespace(ns)); err != nil {
			return false, "", fmt.Errorf("listing UserNotificationOptOut in %s: %w", ns, err)
		}
		for i := range userList.Items {
			item := &userList.Items[i]
			if subjectMatches(item.Spec.Subject, recipient) &&
				targetMatches(item.Spec.Target, scope) &&
				categoryMatches(item.Spec.Categories, category) {
				return true, fmt.Sprintf("user opt-out %q in %s for %s %s", item.Name, ns, item.Spec.Subject.Kind, item.Spec.Subject.Name), nil
			}
		}
	}

	return false, "", nil
}

// candidateNamespaces returns the onboarding namespaces to list opt-outs in for a given scope.
// We always check the project namespace (so project-level opt-outs cascade to sub-resources),
// and additionally the workspace namespace when the scope includes a workspace.
func candidateNamespaces(scope Scope) []string {
	ns := []string{projectNamespace(scope.Project)}
	if scope.Workspace != "" {
		ns = append(ns, workspaceNamespace(scope.Project, scope.Workspace))
	}
	return ns
}

// targetMatches returns true when the ResourceRef's target is the same as or an ancestor of the
// event scope. This implements the cascade rule:
//
//   - A Project target cascades to Workspace and ControlPlane events.
//   - A Workspace target cascades to ControlPlane events within that workspace.
//   - A ControlPlane target matches only that specific ControlPlane event.
func targetMatches(target v1alpha1.ResourceRef, scope Scope) bool {
	switch target.Kind {
	case "Project":
		return scope.Project == target.Name
	case "Workspace":
		return scope.Workspace == target.Name && scope.Workspace != ""
	case "ControlPlane":
		return scope.ControlPlane == target.Name && scope.ControlPlane != ""
	}
	return false
}

// categoryMatches returns true when the category list is empty (mute all) or contains the given
// category.
func categoryMatches(categories []v1alpha1.Category, category v1alpha1.Category) bool {
	if len(categories) == 0 {
		return true
	}
	for _, c := range categories {
		if c == category {
			return true
		}
	}
	return false
}

// subjectMatches returns true when the opt-out subject refers to the same identity as the
// recipient. For ServiceAccount subjects the namespace must also match.
func subjectMatches(specSubject, recipient v1alpha1.Subject) bool {
	if specSubject.Kind != recipient.Kind || specSubject.Name != recipient.Name {
		return false
	}
	if specSubject.Kind == v1alpha1.SubjectKindServiceAccount {
		return specSubject.Namespace == recipient.Namespace
	}
	return true
}

// Deterministic onboarding namespace names. These mirror the consts in
// internal/controller/membership.go; duplicated here to avoid a circular import.
const (
	nsProjectPrefix   = "project-"
	nsWorkspaceMarker = "--ws-"
)

func projectNamespace(project string) string {
	return nsProjectPrefix + project
}

func workspaceNamespace(project, workspace string) string {
	return strings.Join([]string{nsProjectPrefix + project, workspace}, nsWorkspaceMarker)
}
