package controller

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/openmcp-project/controller-utils/pkg/clusters"
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

	"github.com/openmcp-project/platform-service-notifications/api/v1alpha1"
	"github.com/openmcp-project/platform-service-notifications/internal/notify"
	"github.com/openmcp-project/platform-service-notifications/internal/optout"
	"github.com/openmcp-project/platform-service-notifications/internal/store"
)

// managedServiceGVK identifies the catalog resource on the onboarding cluster that lists the
// available service and Crossplane-provider versions for the platform.
var managedServiceGVK = schema.GroupVersionKind{
	Group:   "open-control-plane.io",
	Version: "v1",
	Kind:    "ManagedService",
}

// VersionDigestReconciler watches ManagedService on the onboarding cluster. When the catalog
// of available service versions changes, it locates each ControlPlane that has the service
// installed, resolves its admins, and sends a single aggregated digest per admin — never one
// email per control plane.
type VersionDigestReconciler struct {
	onboarding   *clusters.Cluster
	store        store.Store
	pipeline     *notify.Pipeline
	suppressor   optout.Suppressor
	providerName string
}

func NewVersionDigestReconciler(onboarding *clusters.Cluster, s store.Store, p *notify.Pipeline, suppressor optout.Suppressor, providerName string) *VersionDigestReconciler {
	if suppressor == nil {
		suppressor = optout.NoOpSuppressor{}
	}
	return &VersionDigestReconciler{onboarding: onboarding, store: s, pipeline: p, suppressor: suppressor, providerName: providerName}
}

func (r *VersionDigestReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	logger := logf.FromContext(ctx)

	ms := &unstructured.Unstructured{}
	ms.SetGroupVersionKind(managedServiceGVK)
	if err := r.onboarding.Client().Get(ctx, req.NamespacedName, ms); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	services, _, _ := unstructured.NestedSlice(ms.Object, "spec", "services")
	var errs []error
	for _, svcRaw := range services {
		svc, ok := svcRaw.(map[string]interface{})
		if !ok {
			continue
		}
		name, _, _ := unstructured.NestedString(svc, "name")
		kind, _, _ := unstructured.NestedString(svc, "kind")
		apiVersion, _, _ := unstructured.NestedString(svc, "apiVersion")
		versionsRaw, _, _ := unstructured.NestedSlice(svc, "versions")

		if name == "" || kind == "" || apiVersion == "" {
			logger.V(1).Info("skipping incomplete service entry in ManagedService", "name", name)
			continue
		}
		gvk, err := parseServiceGVK(kind, apiVersion)
		if err != nil {
			logger.Error(err, "skipping service with invalid apiVersion", "service", name)
			continue
		}

		for _, vRaw := range versionsRaw {
			vMap, ok := vRaw.(map[string]interface{})
			if !ok {
				continue
			}
			version, _, _ := unstructured.NestedString(vMap, "version")
			if version == "" {
				continue
			}
			if err := r.notifyForService(ctx, name, version, gvk); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return reconcile.Result{}, errors.Join(errs...)
}

func (r *VersionDigestReconciler) notifyForService(ctx context.Context, serviceName, version string, gvk schema.GroupVersionKind) error {
	cpKeys, err := r.controlPlanesForResource(ctx, gvk)
	if err != nil {
		return fmt.Errorf("listing resources for %s: %w", gvk.String(), err)
	}

	// Aggregate: admin subject -> the control planes that admin can act on, after opt-out.
	perAdmin := map[string][]notify.AffectedControlPlane{}
	adminSubject := map[string]v1alpha1.Subject{}

	for _, cpRef := range cpKeys {
		cp := &cpv2alpha1.ControlPlane{}
		if err := r.onboarding.Client().Get(ctx, cpRef, cp); err != nil {
			if client.IgnoreNotFound(err) == nil {
				continue
			}
			return fmt.Errorf("getting control plane %s: %w", cpRef, err)
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
				return fmt.Errorf("checking opt-out for admin %s on CP %s: %w", admin.Name, cp.Name, err)
			}
			if suppressed {
				continue
			}
			id := subjectID(admin)
			adminSubject[id] = admin
			perAdmin[id] = append(perAdmin[id], entry)
		}
	}

	if len(perAdmin) == 0 {
		return nil
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
			EventKey: strings.Join([]string{"newversion", serviceName, version}, ":"),
			Data: notify.NewVersionDigestData{
				ProductName:           r.pipeline.ProductName(),
				RecipientName:         admin.Name,
				ServiceName:           serviceName,
				NewVersion:            version,
				AffectedControlPlanes: cps,
				ConsoleURL:            r.pipeline.WebAppURL(),
				SupportURL:            r.pipeline.SupportURL(),
			},
		}
		if _, err := r.pipeline.Deliver(ctx, ev); err != nil {
			errs = append(errs, fmt.Errorf("delivering digest to %s: %w", admin.Name, err))
		}
	}
	return errors.Join(errs...)
}

// controlPlanesForResource lists all objects of the given service GVK on the onboarding cluster.
// Each object corresponds to exactly one control plane: the platform creates a service resource
// named and namespaced identically to its ControlPlane (mcp.Name / mcp.Namespace), as done in
// openmcp-operator's controlplane controller (services.go). We therefore map each object back to
// its ControlPlane by (namespace, name).
func (r *VersionDigestReconciler) controlPlanesForResource(ctx context.Context, gvk schema.GroupVersionKind) ([]client.ObjectKey, error) {
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
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(managedServiceGVK)
	return ctrl.NewControllerManagedBy(mgr).
		Named(r.providerName + "-version-digest").
		WatchesRawSource(source.Kind(
			mgr.GetCache(),
			obj,
			&handler.TypedEnqueueRequestForObject[*unstructured.Unstructured]{},
			predicate.TypedGenerationChangedPredicate[*unstructured.Unstructured]{},
		)).
		Complete(r)
}

// parseServiceGVK derives a GVK from the kind and apiVersion fields in a ManagedService services
// entry. apiVersion is "version" for core resources, or "group/version" for CRDs.
func parseServiceGVK(kind, apiVersion string) (schema.GroupVersionKind, error) {
	if apiVersion == "" {
		return schema.GroupVersionKind{}, fmt.Errorf("empty apiVersion for kind %q", kind)
	}
	if idx := strings.IndexByte(apiVersion, '/'); idx >= 0 {
		return schema.GroupVersionKind{Group: apiVersion[:idx], Version: apiVersion[idx+1:], Kind: kind}, nil
	}
	return schema.GroupVersionKind{Version: apiVersion, Kind: kind}, nil
}
