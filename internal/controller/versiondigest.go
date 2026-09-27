package controller

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/openmcp-project/controller-utils/pkg/clusters"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	cpv2alpha1 "github.com/openmcp-project/openmcp-operator/api/core/v2alpha1"
	providerv1alpha1 "github.com/openmcp-project/openmcp-operator/api/provider/v1alpha1"

	"github.com/openmcp-project/platform-service-notifications/api/v1alpha1"
	"github.com/openmcp-project/platform-service-notifications/internal/notify"
	"github.com/openmcp-project/platform-service-notifications/internal/optout"
	"github.com/openmcp-project/platform-service-notifications/internal/store"
)

// VersionDigestReconciler watches ServiceProviders on the platform cluster. When a provider's
// image (version) changes, it computes the ControlPlanes that use the service, resolves their
// admins, and sends a single aggregated digest per admin — never one mail per control plane.
type VersionDigestReconciler struct {
	platform     *clusters.Cluster
	onboarding   *clusters.Cluster
	store        store.Store
	pipeline     *notify.Pipeline
	suppressor   optout.Suppressor
	providerName string
}

func NewVersionDigestReconciler(platform, onboarding *clusters.Cluster, s store.Store, p *notify.Pipeline, suppressor optout.Suppressor, providerName string) *VersionDigestReconciler {
	if suppressor == nil {
		suppressor = optout.NoOpSuppressor{}
	}
	return &VersionDigestReconciler{platform: platform, onboarding: onboarding, store: s, pipeline: p, suppressor: suppressor, providerName: providerName}
}

func (r *VersionDigestReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	logger := logf.FromContext(ctx)

	sp := &providerv1alpha1.ServiceProvider{}
	if err := r.platform.Client().Get(ctx, req.NamespacedName, sp); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	newVersion := parseImageTag(sp.Spec.Image)
	if newVersion == "" {
		logger.V(1).Info("service provider image has no discernible tag; skipping", "image", sp.Spec.Image)
		return reconcile.Result{}, nil
	}
	serviceName := sp.Name

	// Aggregate: admin subject -> the control planes that admin can act on, after opt-out.
	perAdmin := map[string][]notify.AffectedControlPlane{}
	adminSubject := map[string]v1alpha1.Subject{}

	for _, gvk := range sp.Status.Resources {
		affected, err := r.controlPlanesForResource(ctx, gvk)
		if err != nil {
			return reconcile.Result{}, fmt.Errorf("listing resources for %s: %w", gvk.String(), err)
		}
		for _, cpRef := range affected {
			cp := &cpv2alpha1.ControlPlane{}
			if err := r.onboarding.Client().Get(ctx, cpRef, cp); err != nil {
				if client.IgnoreNotFound(err) == nil {
					continue
				}
				return reconcile.Result{}, fmt.Errorf("getting control plane %s: %w", cpRef, err)
			}
			entry := notify.AffectedControlPlane{
				Name:       cp.Name,
				Namespace:  cp.Namespace,
				ConsoleURL: consoleLink(r.pipeline.WebAppURL(), "ControlPlane", cp.Namespace, cp.Name),
			}
			project, workspace := projectWorkspaceFromNamespace(cp.Namespace)
			cpScope := optout.Scope{Project: project, Workspace: workspace, ControlPlane: cp.Name}

			for _, admin := range controlPlaneAdmins(cp) {
				// Check opt-out per (admin, control plane) before adding the CP to the digest.
				suppressed, _, err := r.suppressor.Suppressed(ctx, admin, v1alpha1.CategoryNewServiceVersion, cpScope)
				if err != nil {
					return reconcile.Result{}, fmt.Errorf("checking opt-out for admin %s on CP %s: %w", admin.Name, cp.Name, err)
				}
				if suppressed {
					continue
				}
				id := subjectID(admin)
				adminSubject[id] = admin
				perAdmin[id] = append(perAdmin[id], entry)
			}
		}
	}

	if len(perAdmin) == 0 {
		return reconcile.Result{}, nil
	}

	var errs []error
	for id, cps := range perAdmin {
		admin := adminSubject[id]
		// Emit the event with an empty Scope: opt-out was already applied per-CP above,
		// and the pipeline-level suppressor (NoOpSuppressor for this reconciler) is a no-op.
		ev := notify.Event{
			Category:  v1alpha1.CategoryNewServiceVersion,
			Recipient: admin,
			// One digest per (admin, service, version): the recipient is not re-notified for
			// the same version even if more control planes become affected later.
			EventKey: strings.Join([]string{"newversion", serviceName, newVersion}, ":"),
			Data: notify.NewVersionDigestData{
				ProductName:           r.pipeline.ProductName(),
				RecipientName:         admin.Name,
				ServiceName:           serviceName,
				NewVersion:            newVersion,
				AffectedControlPlanes: cps,
				ConsoleURL:            r.pipeline.WebAppURL(),
				SupportURL:            r.pipeline.SupportURL(),
			},
		}
		if _, err := r.pipeline.Deliver(ctx, ev); err != nil {
			errs = append(errs, fmt.Errorf("delivering digest to %s: %w", admin.Name, err))
		}
	}
	return reconcile.Result{}, errors.Join(errs...)
}

// controlPlanesForResource lists all objects of the given service GVK on the onboarding cluster.
// Each object corresponds to exactly one control plane: the platform creates a service resource
// named and namespaced identically to its ControlPlane (mcp.Name / mcp.Namespace), as done in
// openmcp-operator's controlplane controller (services.go). We therefore map each object back to
// its ControlPlane by (namespace, name).
func (r *VersionDigestReconciler) controlPlanesForResource(ctx context.Context, gvk metav1.GroupVersionKind) ([]client.ObjectKey, error) {
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   gvk.Group,
		Version: gvk.Version,
		Kind:    gvk.Kind + "List",
	})
	if err := r.onboarding.Client().List(ctx, list); err != nil {
		return nil, err
	}
	keys := make([]client.ObjectKey, 0, len(list.Items))
	for i := range list.Items {
		it := &list.Items[i]
		keys = append(keys, client.ObjectKey{Namespace: it.GetNamespace(), Name: it.GetName()})
	}
	return keys, nil
}

func (r *VersionDigestReconciler) SetupWithManager(mgr manager.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named(r.providerName + "-version-digest").
		WatchesRawSource(source.Kind(
			r.platform.Cluster().GetCache(),
			&providerv1alpha1.ServiceProvider{},
			&handler.TypedEnqueueRequestForObject[*providerv1alpha1.ServiceProvider]{},
			predicate.TypedGenerationChangedPredicate[*providerv1alpha1.ServiceProvider]{},
		)).
		Complete(r)
}

// parseImageTag returns the tag portion of a container image reference, or "" if untagged.
// It correctly ignores a registry port (e.g. "registry:5000/img:v1" -> "v1").
func parseImageTag(image string) string {
	lastSlash := strings.LastIndex(image, "/")
	name := image
	if lastSlash >= 0 {
		name = image[lastSlash+1:]
	}
	if idx := strings.LastIndex(name, "@"); idx >= 0 {
		// digest reference; treat the digest as the version identifier
		return name[idx+1:]
	}
	if idx := strings.LastIndex(name, ":"); idx >= 0 {
		return name[idx+1:]
	}
	return ""
}
