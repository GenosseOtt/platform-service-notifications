package store

import (
	"context"
	"time"

	"github.com/openmcp-project/platform-service-notifications/api/v1alpha1"
)

// Store persists the platform user registry, notification-delivery ledger, and preferences.
// It is deliberately backend-agnostic: the only implementation today is Kubernetes CRDs
// (see NewK8sStore), but a database-backed implementation could be dropped in without
// touching the reconcilers.
type Store interface {
	// Claim atomically reserves delivery of n. It returns claimed=true exactly once per
	// unique notification; subsequent calls for the same notification return claimed=false.
	// The returned record is the ledger entry (existing or newly created).
	Claim(ctx context.Context, n Notification) (claimed bool, rec *v1alpha1.NotificationRecord, err error)

	// MarkDelivered records a successful delivery.
	MarkDelivered(ctx context.Context, rec *v1alpha1.NotificationRecord) error
	// MarkFailed records a failed delivery attempt with the given cause.
	MarkFailed(ctx context.Context, rec *v1alpha1.NotificationRecord, cause error) error
	// MarkSuppressed records that delivery was intentionally not attempted (e.g. opt-out).
	MarkSuppressed(ctx context.Context, rec *v1alpha1.NotificationRecord, reason string) error

	// EnsureUserProfile returns the UserProfile for subj, creating it (and stamping FirstSeen)
	// if it does not exist yet. created reports whether this call created it.
	EnsureUserProfile(ctx context.Context, subj v1alpha1.Subject) (profile *v1alpha1.UserProfile, created bool, err error)
	// GetUserProfile returns the UserProfile for subj, or (nil, nil) if none exists.
	GetUserProfile(ctx context.Context, subj v1alpha1.Subject) (*v1alpha1.UserProfile, error)
	// SetEnablementSent stamps the one-time enablement timestamp on the profile.
	SetEnablementSent(ctx context.Context, profile *v1alpha1.UserProfile, at time.Time) error

	// PruneExpired deletes Delivered/Suppressed records older than retention. Returns the count deleted.
	PruneExpired(ctx context.Context, retention time.Duration) (int, error)
}
