package store

import (
	"context"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openmcp-project/platform-service-notifications/api/v1alpha1"
)

// K8sStore implements Store on top of the platform-cluster Kubernetes API.
type K8sStore struct {
	c client.Client
}

// NewK8sStore returns a Store backed by the given platform-cluster client.
func NewK8sStore(c client.Client) *K8sStore {
	return &K8sStore{c: c}
}

var _ Store = (*K8sStore)(nil)

func (s *K8sStore) Claim(ctx context.Context, n Notification) (bool, *v1alpha1.NotificationRecord, error) {
	rec := &v1alpha1.NotificationRecord{
		ObjectMeta: metav1.ObjectMeta{Name: n.RecordName()},
		Spec: v1alpha1.NotificationRecordSpec{
			Category:         n.Category,
			Channel:          n.Channel,
			Recipient:        n.Recipient,
			RecipientAddress: n.RecipientAddress,
			EventKey:         n.EventKey,
		},
	}
	err := s.c.Create(ctx, rec)
	switch {
	case err == nil:
		rec.Status.Phase = v1alpha1.RecordPhasePending
		if serr := s.c.Status().Update(ctx, rec); serr != nil {
			return true, rec, serr
		}
		return true, rec, nil
	case apierrors.IsAlreadyExists(err):
		existing := &v1alpha1.NotificationRecord{}
		if gerr := s.c.Get(ctx, types.NamespacedName{Name: n.RecordName()}, existing); gerr != nil {
			return false, nil, gerr
		}
		// A record in a terminal state (delivered or intentionally suppressed) is done: do not
		// re-deliver. A Pending or Failed record is re-claimable so a prior failed attempt (or
		// one interrupted before completion) can be retried.
		switch existing.Status.Phase {
		case v1alpha1.RecordPhaseDelivered, v1alpha1.RecordPhaseSuppressed:
			return false, existing, nil
		default:
			return true, existing, nil
		}
	default:
		return false, nil, err
	}
}

func (s *K8sStore) MarkDelivered(ctx context.Context, rec *v1alpha1.NotificationRecord) error {
	now := metav1.Now()
	rec.Status.Phase = v1alpha1.RecordPhaseDelivered
	rec.Status.Attempts++
	rec.Status.LastError = ""
	rec.Status.SentAt = &now
	return s.c.Status().Update(ctx, rec)
}

func (s *K8sStore) MarkFailed(ctx context.Context, rec *v1alpha1.NotificationRecord, cause error) error {
	rec.Status.Phase = v1alpha1.RecordPhaseFailed
	rec.Status.Attempts++
	if cause != nil {
		rec.Status.LastError = cause.Error()
	}
	return s.c.Status().Update(ctx, rec)
}

func (s *K8sStore) MarkSuppressed(ctx context.Context, rec *v1alpha1.NotificationRecord, reason string) error {
	rec.Status.Phase = v1alpha1.RecordPhaseSuppressed
	rec.Status.LastError = reason
	return s.c.Status().Update(ctx, rec)
}

func (s *K8sStore) EnsureUserProfile(ctx context.Context, subj v1alpha1.Subject) (*v1alpha1.UserProfile, bool, error) {
	name := ProfileName(subj)
	profile := &v1alpha1.UserProfile{}
	err := s.c.Get(ctx, types.NamespacedName{Name: name}, profile)
	if err == nil {
		return profile, false, nil
	}
	if !apierrors.IsNotFound(err) {
		return nil, false, err
	}

	profile = &v1alpha1.UserProfile{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec:       v1alpha1.UserProfileSpec{Subject: subj},
	}
	if cerr := s.c.Create(ctx, profile); cerr != nil {
		if apierrors.IsAlreadyExists(cerr) {
			// Lost a race; fetch and treat as not-created.
			if gerr := s.c.Get(ctx, types.NamespacedName{Name: name}, profile); gerr != nil {
				return nil, false, gerr
			}
			return profile, false, nil
		}
		return nil, false, cerr
	}

	now := metav1.Now()
	profile.Status.FirstSeen = &now
	if serr := s.c.Status().Update(ctx, profile); serr != nil {
		return profile, true, serr
	}
	return profile, true, nil
}

func (s *K8sStore) GetUserProfile(ctx context.Context, subj v1alpha1.Subject) (*v1alpha1.UserProfile, error) {
	profile := &v1alpha1.UserProfile{}
	err := s.c.Get(ctx, types.NamespacedName{Name: ProfileName(subj)}, profile)
	if apierrors.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return profile, nil
}

func (s *K8sStore) SetEnablementSent(ctx context.Context, profile *v1alpha1.UserProfile, at time.Time) error {
	t := metav1.NewTime(at)
	profile.Status.EnablementSentAt = &t
	return s.c.Status().Update(ctx, profile)
}

func (s *K8sStore) PruneExpired(ctx context.Context, retention time.Duration) (int, error) {
	list := &v1alpha1.NotificationRecordList{}
	if err := s.c.List(ctx, list); err != nil {
		return 0, err
	}
	cutoff := time.Now().Add(-retention)
	deleted := 0
	for i := range list.Items {
		rec := &list.Items[i]
		if rec.Status.Phase != v1alpha1.RecordPhaseDelivered && rec.Status.Phase != v1alpha1.RecordPhaseSuppressed {
			continue
		}
		ts := rec.CreationTimestamp.Time
		if rec.Status.SentAt != nil {
			ts = rec.Status.SentAt.Time
		}
		if ts.After(cutoff) {
			continue
		}
		if err := s.c.Delete(ctx, rec); err != nil && !apierrors.IsNotFound(err) {
			return deleted, err
		}
		deleted++
	}
	return deleted, nil
}
