package notify

import (
	"context"
	"errors"
	"testing"

	"github.com/openmcp-project/platform-service-notifications/api/v1alpha1"
)

func TestDeliver_HappyPath(t *testing.T) {
	s := newFakeStore()
	n := &fakeNotifier{}
	p := newTestPipeline(s, n, defaultSettings())

	ev := Event{Category: v1alpha1.CategoryMembershipAdded, Recipient: userSubject("alice@example.com"), EventKey: "k1"}
	results, err := p.Deliver(context.Background(), ev)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 || results[0].Outcome != OutcomeDelivered {
		t.Fatalf("expected one Delivered result, got %+v", results)
	}
	if len(n.sent) != 1 {
		t.Fatalf("expected 1 sent message, got %d", len(n.sent))
	}
	if n.sent[0].To != "alice@example.com" || n.sent[0].Subject != fakeRenderedSubject || n.sent[0].HTML != fakeRenderedHTML || n.sent[0].Text != "hi" {
		t.Fatalf("message not assembled as expected: %+v", n.sent[0])
	}
	if s.delivered != 1 {
		t.Fatalf("expected MarkDelivered once, got %d", s.delivered)
	}
}

func TestDeliver_CategoryDisabled(t *testing.T) {
	s := newFakeStore()
	n := &fakeNotifier{}
	set := defaultSettings()
	set.EnabledCategories = []v1alpha1.Category{v1alpha1.CategoryUserEnablement} // membership not enabled
	p := newTestPipeline(s, n, set)

	results, err := p.Deliver(context.Background(), Event{Category: v1alpha1.CategoryMembershipAdded, Recipient: userSubject("a@x.io"), EventKey: "k"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 || results[0].Outcome != OutcomeSkipped {
		t.Fatalf("expected Skipped, got %+v", results)
	}
	if len(s.claims) != 0 || len(n.sent) != 0 {
		t.Fatalf("nothing should be claimed or sent for a disabled category")
	}
}

func TestDeliver_OptOutAll(t *testing.T) {
	// When the suppressor signals suppression, the pipeline records Suppressed and does not send.
	s := newFakeStore()
	n := &fakeNotifier{}
	sup := &fakeSuppressor{suppressed: true, reason: "resource opt-out in project-p"}
	p := newTestPipelineWithSuppressor(s, n, defaultSettings(), sup)

	subj := userSubject("a@x.io")
	results, err := p.Deliver(context.Background(), Event{Category: v1alpha1.CategoryMembershipAdded, Recipient: subj, EventKey: "k"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 || results[0].Outcome != OutcomeSuppressed {
		t.Fatalf("expected Suppressed, got %+v", results)
	}
	if s.suppressed != 1 {
		t.Fatalf("expected MarkSuppressed once, got %d", s.suppressed)
	}
	if len(n.sent) != 0 {
		t.Fatalf("nothing should be sent when suppressed")
	}
}

func TestDeliver_OptOutCategoryOnly(t *testing.T) {
	// When the suppressor only suppresses a specific category, other categories are still delivered.
	s := newFakeStore()
	n := &fakeNotifier{}
	sup := &fakeSuppressor{suppressed: true, reason: "user opt-out", onlyCategory: v1alpha1.CategoryMembershipAdded}
	subj := userSubject("a@x.io")
	p := newTestPipelineWithSuppressor(s, n, defaultSettings(), sup)

	// Opted-out category is suppressed.
	res, _ := p.Deliver(context.Background(), Event{Category: v1alpha1.CategoryMembershipAdded, Recipient: subj, EventKey: "k"})
	if res[0].Outcome != OutcomeSuppressed {
		t.Fatalf("expected Suppressed for opted-out category, got %+v", res)
	}
	// A different category is still delivered.
	res, _ = p.Deliver(context.Background(), Event{Category: v1alpha1.CategoryUserEnablement, Recipient: subj, EventKey: "k2"})
	if res[0].Outcome != OutcomeDelivered {
		t.Fatalf("expected Delivered for non-opted category, got %+v", res)
	}
}

func TestDeliver_Duplicate(t *testing.T) {
	s := newFakeStore()
	n := &fakeNotifier{}
	p := newTestPipeline(s, n, defaultSettings())
	ev := Event{Category: v1alpha1.CategoryMembershipAdded, Recipient: userSubject("a@x.io"), EventKey: "k"}
	// Mark this notification terminal so Claim returns claimed=false.
	dk := storeDedupKey(ev, v1alpha1.ChannelEmail)
	s.terminal[dk] = true

	res, err := p.Deliver(context.Background(), ev)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res[0].Outcome != OutcomeDuplicate {
		t.Fatalf("expected Duplicate, got %+v", res)
	}
	if len(n.sent) != 0 || s.delivered != 0 {
		t.Fatalf("duplicate must not send or mark delivered")
	}
}

func TestDeliver_SendFailure(t *testing.T) {
	s := newFakeStore()
	n := &fakeNotifier{sendErr: errors.New("smtp down")}
	p := newTestPipeline(s, n, defaultSettings())

	res, err := p.Deliver(context.Background(), Event{Category: v1alpha1.CategoryMembershipAdded, Recipient: userSubject("a@x.io"), EventKey: "k"})
	if err == nil {
		t.Fatalf("expected error on send failure")
	}
	if res[0].Outcome != OutcomeFailed {
		t.Fatalf("expected Failed, got %+v", res)
	}
	if s.failed != 1 {
		t.Fatalf("expected MarkFailed once, got %d", s.failed)
	}
}

func TestDeliver_AddressResolution(t *testing.T) {
	t.Run("username is email", func(t *testing.T) {
		s := newFakeStore()
		n := &fakeNotifier{}
		p := newTestPipeline(s, n, defaultSettings())
		res, _ := p.Deliver(context.Background(), Event{Category: v1alpha1.CategoryMembershipAdded, Recipient: userSubject("bob@x.io"), EventKey: "k"})
		if res[0].Outcome != OutcomeDelivered || n.sent[0].To != "bob@x.io" {
			t.Fatalf("expected delivery to bob@x.io, got %+v / sent %+v", res, n.sent)
		}
	})

	t.Run("profile email overrides", func(t *testing.T) {
		s := newFakeStore()
		subj := userSubject("bob")
		s.profiles["bob"] = &v1alpha1.UserProfile{Spec: v1alpha1.UserProfileSpec{Subject: subj, Email: "override@x.io"}}
		n := &fakeNotifier{}
		p := newTestPipeline(s, n, defaultSettings())
		_, _ = p.Deliver(context.Background(), Event{Category: v1alpha1.CategoryMembershipAdded, Recipient: subj, EventKey: "k"})
		if len(n.sent) != 1 || n.sent[0].To != "override@x.io" {
			t.Fatalf("expected delivery to override@x.io, got %+v", n.sent)
		}
	})

	t.Run("no address resolvable", func(t *testing.T) {
		s := newFakeStore()
		n := &fakeNotifier{}
		set := defaultSettings()
		set.UsernameIsEmail = false // and no profile email
		p := newTestPipeline(s, n, set)
		res, err := p.Deliver(context.Background(), Event{Category: v1alpha1.CategoryMembershipAdded, Recipient: userSubject("bob"), EventKey: "k"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res[0].Outcome != OutcomeSkipped || len(n.sent) != 0 {
			t.Fatalf("expected Skipped with no send, got %+v", res)
		}
	})
}

func TestSettingsFromConfig_Defaults(t *testing.T) {
	s := SettingsFromConfig(v1alpha1.NotificationConfigSpec{})
	if !s.UsernameIsEmail {
		t.Errorf("UsernameIsEmail should default to true")
	}
	if len(s.EnabledChannels) != 1 || s.EnabledChannels[0] != v1alpha1.ChannelEmail {
		t.Errorf("EnabledChannels should default to [Email], got %v", s.EnabledChannels)
	}

	f := false
	s = SettingsFromConfig(v1alpha1.NotificationConfigSpec{UsernameIsEmail: &f})
	if s.UsernameIsEmail {
		t.Errorf("UsernameIsEmail should honor explicit false")
	}
}

// storeDedupKey mirrors store.Notification.DedupKey for the given event/channel so tests
// can pre-seed a terminal record without importing internal ordering details.
func storeDedupKey(ev Event, ch v1alpha1.Channel) string {
	// Must match store.Notification.DedupKey field ordering.
	return string(ev.Category) + "|" + string(ch) + "|" + string(ev.Recipient.Kind) + "|" +
		ev.Recipient.Namespace + "|" + ev.Recipient.Name + "|" + ev.EventKey
}
