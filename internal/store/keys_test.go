package store

import (
	"strings"
	"testing"

	"github.com/openmcp-project/platform-service-notifications/api/v1alpha1"
)

func testNotification() Notification {
	return Notification{
		Category:         v1alpha1.CategoryMembershipAdded,
		Channel:          v1alpha1.ChannelEmail,
		Recipient:        v1alpha1.Subject{Kind: v1alpha1.SubjectKindUser, Namespace: "ns", Name: "a@x.io"},
		RecipientAddress: "a@x.io",
		EventKey:         "membership:Project:ns/p:User/ns/a@x.io",
	}
}

func TestDedupKey_Deterministic(t *testing.T) {
	a := testNotification().DedupKey()
	b := testNotification().DedupKey()
	if a != b {
		t.Fatalf("DedupKey not deterministic: %q vs %q", a, b)
	}
	// RecipientAddress is deliberately NOT part of the identity: changing only the resolved
	// address must not create a second notification for the same logical event.
	n := testNotification()
	n.RecipientAddress = "different@x.io"
	if n.DedupKey() != a {
		t.Error("DedupKey must not depend on RecipientAddress")
	}
}

func TestDedupKey_DistinguishesFields(t *testing.T) {
	base := testNotification()
	variants := []func(*Notification){
		func(n *Notification) { n.Category = v1alpha1.CategoryUserEnablement },
		func(n *Notification) { n.Channel = v1alpha1.ChannelSlack },
		func(n *Notification) { n.Recipient.Name = "b@x.io" },
		func(n *Notification) { n.Recipient.Namespace = "other" },
		func(n *Notification) { n.Recipient.Kind = v1alpha1.SubjectKindGroup },
		func(n *Notification) { n.EventKey = "other" },
	}
	seen := map[string]bool{base.DedupKey(): true}
	for i, mut := range variants {
		n := testNotification()
		mut(&n)
		k := n.DedupKey()
		if seen[k] {
			t.Errorf("variant %d produced a colliding dedup key: %q", i, k)
		}
		seen[k] = true
	}
}

func TestRecordName_Valid(t *testing.T) {
	name := testNotification().RecordName()
	if !strings.HasPrefix(name, "nr-") {
		t.Errorf("RecordName should be prefixed nr-, got %q", name)
	}
	if len(name) != 43 { // "nr-" + 40 hex chars
		t.Errorf("RecordName length = %d, want 43 (%q)", len(name), name)
	}
	if name != testNotification().RecordName() {
		t.Error("RecordName not deterministic")
	}
	if !isDNS1123Subdomain(name) {
		t.Errorf("RecordName %q is not RFC1123-compliant", name)
	}
}

func TestProfileName_Valid(t *testing.T) {
	s := v1alpha1.Subject{Kind: v1alpha1.SubjectKindUser, Namespace: "ns", Name: "a@x.io"}
	name := ProfileName(s)
	if !strings.HasPrefix(name, "up-") {
		t.Errorf("ProfileName should be prefixed up-, got %q", name)
	}
	if name != ProfileName(s) {
		t.Error("ProfileName not deterministic")
	}
	// Different subjects -> different names.
	other := s
	other.Name = "b@x.io"
	if ProfileName(other) == name {
		t.Error("distinct subjects must map to distinct profile names")
	}
	if !isDNS1123Subdomain(name) {
		t.Errorf("ProfileName %q is not RFC1123-compliant", name)
	}
}

// isDNS1123Subdomain performs a minimal check that a name is a lowercase alphanumeric/'-'
// string — sufficient to guard against the email/'@'/uppercase characters leaking into the
// derived object name.
func isDNS1123Subdomain(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-') {
			return false
		}
	}
	return true
}
