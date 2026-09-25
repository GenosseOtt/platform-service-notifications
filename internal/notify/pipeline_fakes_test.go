package notify

import (
	"context"
	"time"

	"github.com/openmcp-project/platform-service-notifications/api/v1alpha1"
	"github.com/openmcp-project/platform-service-notifications/internal/store"
)

// --- fakes ---

type fakeStore struct {
	profiles map[string]*v1alpha1.UserProfile // keyed by subject name
	// claimedKeys records dedup keys that are already terminal (return claimed=false).
	terminal map[string]bool

	claims      []store.Notification
	delivered   int
	failed      int
	suppressed  int
	lastFailErr error
}

func newFakeStore() *fakeStore {
	return &fakeStore{profiles: map[string]*v1alpha1.UserProfile{}, terminal: map[string]bool{}}
}

func (f *fakeStore) Claim(_ context.Context, n store.Notification) (bool, *v1alpha1.NotificationRecord, error) {
	f.claims = append(f.claims, n)
	rec := &v1alpha1.NotificationRecord{}
	if f.terminal[n.DedupKey()] {
		return false, rec, nil
	}
	return true, rec, nil
}
func (f *fakeStore) MarkDelivered(_ context.Context, _ *v1alpha1.NotificationRecord) error {
	f.delivered++
	return nil
}
func (f *fakeStore) MarkFailed(_ context.Context, _ *v1alpha1.NotificationRecord, cause error) error {
	f.failed++
	f.lastFailErr = cause
	return nil
}
func (f *fakeStore) MarkSuppressed(_ context.Context, _ *v1alpha1.NotificationRecord, _ string) error {
	f.suppressed++
	return nil
}
func (f *fakeStore) EnsureUserProfile(_ context.Context, _ v1alpha1.Subject) (*v1alpha1.UserProfile, bool, error) {
	return nil, false, nil
}
func (f *fakeStore) GetUserProfile(_ context.Context, subj v1alpha1.Subject) (*v1alpha1.UserProfile, error) {
	return f.profiles[subj.Name], nil
}
func (f *fakeStore) SetEnablementSent(_ context.Context, _ *v1alpha1.UserProfile, _ time.Time) error {
	return nil
}
func (f *fakeStore) PruneExpired(_ context.Context, _ time.Duration) (int, error) { return 0, nil }

type fakeNotifier struct {
	sent    []Message
	sendErr error
}

func (n *fakeNotifier) Channel() v1alpha1.Channel { return v1alpha1.ChannelEmail }
func (n *fakeNotifier) Send(_ context.Context, m Message) error {
	if n.sendErr != nil {
		return n.sendErr
	}
	n.sent = append(n.sent, m)
	return nil
}

type fakeRenderer struct {
	out Rendered
	err error
}

func (r *fakeRenderer) Render(_ v1alpha1.Category, _ any) (Rendered, error) {
	return r.out, r.err
}

func userSubject(name string) v1alpha1.Subject {
	return v1alpha1.Subject{Kind: v1alpha1.SubjectKindUser, Name: name}
}

func defaultSettings() Settings {
	return Settings{EnabledChannels: []v1alpha1.Channel{v1alpha1.ChannelEmail}, UsernameIsEmail: true}
}

func newTestPipeline(s store.Store, n Notifier, set Settings) (*Pipeline, *fakeRenderer) {
	r := &fakeRenderer{out: Rendered{Subject: "subj", HTML: "<b>hi</b>", Text: "hi"}}
	return NewPipeline(s, r, set, n), r
}
