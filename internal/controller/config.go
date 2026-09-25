package controller

import (
	"context"
	"fmt"

	"github.com/openmcp-project/controller-utils/pkg/clusters"
	ctrlutils "github.com/openmcp-project/controller-utils/pkg/controller"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"github.com/openmcp-project/platform-service-notifications/api/v1alpha1"
	"github.com/openmcp-project/platform-service-notifications/internal/notify"
	"github.com/openmcp-project/platform-service-notifications/internal/notify/email"
)

// ConfigReconciler watches the singleton NotificationConfig (and the referenced SMTP Secret) on
// the platform cluster and pushes the resolved settings and credentials into the live delivery
// pipeline and email sender. This is how configuration and credentials hot-reload at runtime.
type ConfigReconciler struct {
	platform          *clusters.Cluster
	pipeline          *notify.Pipeline
	sender            *email.Sender
	providerName      string
	providerNamespace string
}

func NewConfigReconciler(platform *clusters.Cluster, p *notify.Pipeline, sender *email.Sender, providerName, providerNamespace string) *ConfigReconciler {
	return &ConfigReconciler{platform: platform, pipeline: p, sender: sender, providerName: providerName, providerNamespace: providerNamespace}
}

func (r *ConfigReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	logger := logf.FromContext(ctx)

	cfg := &v1alpha1.NotificationConfig{}
	if err := r.platform.Client().Get(ctx, types.NamespacedName{Name: r.providerName}, cfg); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	// Push notifier-relevant settings into the pipeline.
	r.pipeline.SetSettings(notify.SettingsFromConfig(cfg.Spec))

	// Resolve and push email configuration/credentials into the sender.
	if cfg.Spec.Email != nil {
		emailCfg, err := r.resolveEmailConfig(ctx, cfg.Spec.Email)
		if err != nil {
			r.setStatus(ctx, cfg, metav1.ConditionFalse, "EmailConfigError", err.Error())
			return reconcile.Result{}, fmt.Errorf("resolving email config: %w", err)
		}
		r.sender.SetConfig(emailCfg)
	}

	r.setStatus(ctx, cfg, metav1.ConditionTrue, "ConfigApplied", "notification configuration applied")
	logger.Info("notification configuration applied")
	return reconcile.Result{}, nil
}

// resolveEmailConfig builds the sender configuration, reading SMTP credentials from the
// referenced Secret when one is configured.
func (r *ConfigReconciler) resolveEmailConfig(ctx context.Context, ec *v1alpha1.EmailConfig) (email.Config, error) {
	startTLS := true
	if ec.StartTLS != nil {
		startTLS = *ec.StartTLS
	}
	out := email.Config{
		Host:          ec.Host,
		Port:          ec.Port,
		StartTLS:      startTLS,
		SenderAddress: ec.SenderAddress,
		SenderName:    ec.SenderName,
		ReplyTo:       ec.ReplyTo,
	}
	if ec.SecretRef == nil {
		return out, nil
	}

	ns := ec.SecretRef.Namespace
	if ns == "" {
		ns = r.providerNamespace
	}
	secret := &corev1.Secret{}
	if err := r.platform.Client().Get(ctx, types.NamespacedName{Namespace: ns, Name: ec.SecretRef.Name}, secret); err != nil {
		return email.Config{}, fmt.Errorf("getting smtp secret %s/%s: %w", ns, ec.SecretRef.Name, err)
	}
	out.Username = string(secret.Data["username"])
	out.Password = string(secret.Data["password"])
	return out, nil
}

func (r *ConfigReconciler) setStatus(ctx context.Context, cfg *v1alpha1.NotificationConfig, status metav1.ConditionStatus, reason, msg string) {
	meta.SetStatusCondition(&cfg.Status.Conditions, metav1.Condition{
		Type:    "Ready",
		Status:  status,
		Reason:  reason,
		Message: msg,
	})
	cfg.Status.ObservedGeneration = cfg.GetGeneration()
	if status == metav1.ConditionTrue {
		cfg.Status.Phase = "Ready"
	} else {
		cfg.Status.Phase = "Error"
	}
	if err := r.platform.Client().Status().Update(ctx, cfg); err != nil {
		logf.FromContext(ctx).Error(err, "failed to update NotificationConfig status")
	}
}

func (r *ConfigReconciler) SetupWithManager(mgr manager.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named(r.providerName + "-config").
		WatchesRawSource(source.Kind(
			r.platform.Cluster().GetCache(),
			&v1alpha1.NotificationConfig{},
			handler.TypedEnqueueRequestsFromMapFunc(r.enqueueConfig()),
			ctrlutils.ToTypedPredicate[*v1alpha1.NotificationConfig](ctrlutils.ExactNamePredicate(r.providerName, "")),
		)).
		// Re-resolve credentials when a Secret in the provider namespace changes.
		WatchesRawSource(source.Kind(
			r.platform.Cluster().GetCache(),
			&corev1.Secret{},
			handler.TypedEnqueueRequestsFromMapFunc(r.enqueueConfigForSecret()),
		)).
		Complete(r)
}

// enqueueConfig maps the singleton NotificationConfig to its own request.
func (r *ConfigReconciler) enqueueConfig() func(context.Context, *v1alpha1.NotificationConfig) []reconcile.Request {
	return func(context.Context, *v1alpha1.NotificationConfig) []reconcile.Request {
		return []reconcile.Request{{NamespacedName: types.NamespacedName{Name: r.providerName}}}
	}
}

// enqueueConfigForSecret enqueues the config only for secrets in the provider namespace.
func (r *ConfigReconciler) enqueueConfigForSecret() func(context.Context, *corev1.Secret) []reconcile.Request {
	return func(_ context.Context, s *corev1.Secret) []reconcile.Request {
		if s.GetNamespace() != r.providerNamespace {
			return nil
		}
		return []reconcile.Request{{NamespacedName: types.NamespacedName{Name: r.providerName}}}
	}
}
