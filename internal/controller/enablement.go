package controller

import (
	"context"
	"fmt"
	"time"

	"github.com/openmcp-project/controller-utils/pkg/clusters"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"github.com/openmcp-project/platform-service-notifications/api/v1alpha1"
	"github.com/openmcp-project/platform-service-notifications/internal/notify"
	"github.com/openmcp-project/platform-service-notifications/internal/store"
)

// EnablementReconciler watches UserProfiles on the platform cluster and sends the one-time
// welcome/enablement notification the first time a profile is seen.
type EnablementReconciler struct {
	platform     *clusters.Cluster
	store        store.Store
	pipeline     *notify.Pipeline
	providerName string
}

func NewEnablementReconciler(platform *clusters.Cluster, s store.Store, p *notify.Pipeline, providerName string) *EnablementReconciler {
	return &EnablementReconciler{platform: platform, store: s, pipeline: p, providerName: providerName}
}

func (r *EnablementReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	logger := logf.FromContext(ctx)

	profile := &v1alpha1.UserProfile{}
	if err := r.platform.Client().Get(ctx, req.NamespacedName, profile); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	// Already enabled: nothing to do.
	if profile.Status.EnablementSentAt != nil {
		return reconcile.Result{}, nil
	}
	if !notifiableSubject(profile.Spec.Subject) {
		return reconcile.Result{}, nil
	}

	ev := notify.Event{
		Category:  v1alpha1.CategoryUserEnablement,
		Recipient: profile.Spec.Subject,
		EventKey:  "enablement:" + subjectID(profile.Spec.Subject),
		Data: notify.UserEnablementData{
			ProductName:   r.pipeline.ProductName(),
			RecipientName: profile.Spec.Subject.Name,
			ConsoleURL:    r.pipeline.WebAppURL(),
			DocsURL:       r.pipeline.DocsURL(),
			SupportURL:    r.pipeline.SupportURL(),
		},
	}
	if _, err := r.pipeline.Deliver(ctx, ev); err != nil {
		// Delivery failed; requeue and try again without stamping the profile.
		return reconcile.Result{}, fmt.Errorf("delivering enablement notification: %w", err)
	}

	// The pipeline handled the event (delivered, suppressed, or already recorded). Stamp the
	// profile so we do not re-evaluate it on every future reconcile.
	if err := r.store.SetEnablementSent(ctx, profile, time.Now()); err != nil {
		return reconcile.Result{}, fmt.Errorf("stamping enablement timestamp: %w", err)
	}
	logger.Info("enablement notification processed", "subject", profile.Spec.Subject.Name)
	return reconcile.Result{}, nil
}

func (r *EnablementReconciler) SetupWithManager(mgr manager.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named(r.providerName + "-enablement").
		WatchesRawSource(source.Kind(
			r.platform.Cluster().GetCache(),
			&v1alpha1.UserProfile{},
			&handler.TypedEnqueueRequestForObject[*v1alpha1.UserProfile]{},
			predicate.TypedGenerationChangedPredicate[*v1alpha1.UserProfile]{},
		)).
		Complete(r)
}
