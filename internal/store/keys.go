package store

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/openmcp-project/platform-service-notifications/api/v1alpha1"
)

// Notification is the intent to deliver a single notification to a single recipient
// over a single channel. It is the unit of deduplication.
type Notification struct {
	Category         v1alpha1.Category
	Channel          v1alpha1.Channel
	Recipient        v1alpha1.Subject
	RecipientAddress string
	// EventKey uniquely identifies the underlying event this notification is about.
	// Two notifications with the same (Recipient, Category, Channel, EventKey) are the
	// same notification and must be delivered at most once.
	EventKey string
}

// DedupKey is the canonical, stable identity used for deduplication.
func (n Notification) DedupKey() string {
	return strings.Join([]string{
		string(n.Category),
		string(n.Channel),
		string(n.Recipient.Kind),
		n.Recipient.Namespace,
		n.Recipient.Name,
		n.EventKey,
	}, "|")
}

// RecordName is the deterministic, RFC1123-compliant object name derived from the dedup key.
// Because the name is a pure function of the dedup key, a Create either succeeds (first time)
// or fails with AlreadyExists (duplicate) — this is the atomic dedup gate.
func (n Notification) RecordName() string {
	sum := sha256.Sum256([]byte(n.DedupKey()))
	return "nr-" + hex.EncodeToString(sum[:])[:40]
}

// ProfileName returns the deterministic cluster-scoped name for a subject's UserProfile.
func ProfileName(s v1alpha1.Subject) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%s", s.Kind, s.Namespace, s.Name)))
	return "up-" + hex.EncodeToString(sum[:])[:40]
}
