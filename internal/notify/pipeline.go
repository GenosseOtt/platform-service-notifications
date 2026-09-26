package notify

import (
	"context"
	"fmt"
	"sync"

	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/openmcp-project/platform-service-notifications/api/v1alpha1"
	"github.com/openmcp-project/platform-service-notifications/internal/optout"
	"github.com/openmcp-project/platform-service-notifications/internal/store"
)

// Event is the intent to notify a single recipient about a single platform activity.
// Data is the typed template payload for the category (see internal/notify/templatedata.go).
type Event struct {
	Category  v1alpha1.Category
	Recipient v1alpha1.Subject
	// EventKey uniquely identifies the underlying activity, so the same event is delivered
	// at most once per recipient/channel (see store.Notification).
	EventKey string
	Data     any
	// Scope is the resource context of the event, used to evaluate opt-out resources on the
	// onboarding cluster. An empty Scope (Project=="") skips the onboarding opt-out check;
	// this is the correct behaviour for UserEnablement (no resource scope) and for pre-filtered
	// NewServiceVersion events (opt-out is resolved per-CP in the reconciler before Deliver is
	// called).
	Scope optout.Scope
}

// Outcome is the result of attempting delivery on one channel.
type Outcome string

const (
	// OutcomeDelivered means the message was sent and recorded.
	OutcomeDelivered Outcome = "Delivered"
	// OutcomeSuppressed means delivery was intentionally skipped (opt-out) and recorded.
	OutcomeSuppressed Outcome = "Suppressed"
	// OutcomeDuplicate means this notification was already claimed by a prior delivery.
	OutcomeDuplicate Outcome = "Duplicate"
	// OutcomeFailed means delivery was attempted but failed; the caller should requeue.
	OutcomeFailed Outcome = "Failed"
	// OutcomeSkipped means the category or channel is not enabled, or no address was resolvable.
	OutcomeSkipped Outcome = "Skipped"
)

// ChannelResult reports the outcome of one channel for one event.
type ChannelResult struct {
	Channel v1alpha1.Channel
	Outcome Outcome
	Err     error
}

// Settings is the resolved, notifier-relevant slice of NotificationConfig. It is a value
// snapshot so it can be swapped atomically when the config changes.
type Settings struct {
	// EnabledCategories restricts which categories notify; empty means all.
	EnabledCategories []v1alpha1.Category
	// EnabledChannels is the default channel set; empty means [Email].
	EnabledChannels []v1alpha1.Channel
	// UsernameIsEmail allows using a User subject's Name directly as an email address.
	UsernameIsEmail bool
	// WebAppURL is the base URL of the platform web UI, used to build deep links
	// (available to templates via their data).
	WebAppURL string
	// ProductName is the platform/product name shown in notifications. Defaults to
	// "Open Control Plane" when unset.
	ProductName string
	// DocsURL is the user documentation link surfaced in the enablement email.
	DocsURL string
}

// DefaultProductName is used when the config does not set a product name.
const DefaultProductName = "Open Control Plane"

// SettingsFromConfig derives Settings from a NotificationConfig spec, applying defaults.
func SettingsFromConfig(spec v1alpha1.NotificationConfigSpec) Settings {
	usernameIsEmail := true
	if spec.UsernameIsEmail != nil {
		usernameIsEmail = *spec.UsernameIsEmail
	}
	channels := spec.EnabledChannels
	if len(channels) == 0 {
		channels = []v1alpha1.Channel{v1alpha1.ChannelEmail}
	}
	productName := spec.ProductName
	if productName == "" {
		productName = DefaultProductName
	}
	return Settings{
		EnabledCategories: spec.EnabledCategories,
		EnabledChannels:   channels,
		UsernameIsEmail:   usernameIsEmail,
		WebAppURL:         spec.WebAppURL,
		ProductName:       productName,
		DocsURL:           spec.DocsURL,
	}
}

func (s Settings) categoryEnabled(c v1alpha1.Category) bool {
	if len(s.EnabledCategories) == 0 {
		return true
	}
	for _, e := range s.EnabledCategories {
		if e == c {
			return true
		}
	}
	return false
}

// Pipeline is the shared delivery path used by every reconciler: it resolves recipient
// preferences and addresses, deduplicates via the Store, renders, sends, and records the
// outcome. Notifiers and settings can be swapped at runtime as config changes.
type Pipeline struct {
	store      store.Store
	renderer   Renderer
	suppressor optout.Suppressor

	mu        sync.RWMutex
	settings  Settings
	notifiers map[v1alpha1.Channel]Notifier
}

// NewPipeline constructs a Pipeline. When suppressor is nil a no-op suppressor is used
// (no onboarding opt-out checks). Notifiers are registered by their Channel().
func NewPipeline(s store.Store, r Renderer, settings Settings, suppressor optout.Suppressor, notifiers ...Notifier) *Pipeline {
	if suppressor == nil {
		suppressor = optout.NoOpSuppressor{}
	}
	p := &Pipeline{
		store:      s,
		renderer:   r,
		suppressor: suppressor,
		settings:   settings,
		notifiers:  make(map[v1alpha1.Channel]Notifier, len(notifiers)),
	}
	for _, n := range notifiers {
		p.notifiers[n.Channel()] = n
	}
	return p
}

// SetSettings atomically replaces the resolved config settings.
func (p *Pipeline) SetSettings(s Settings) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.settings = s
}

// WebAppURL returns the currently configured platform web-UI base URL (used to build deep links).
func (p *Pipeline) WebAppURL() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.settings.WebAppURL
}

// ProductName returns the configured product name, or the default when unset.
func (p *Pipeline) ProductName() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.settings.ProductName == "" {
		return DefaultProductName
	}
	return p.settings.ProductName
}

// DocsURL returns the configured user-documentation link.
func (p *Pipeline) DocsURL() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.settings.DocsURL
}

// Deliver processes one event for one recipient across the recipient's effective channels.
// It never returns an error for a duplicate, suppression, or disabled category/channel; it
// returns an error only when at least one channel failed delivery (so the caller can requeue).
func (p *Pipeline) Deliver(ctx context.Context, ev Event) ([]ChannelResult, error) {
	logger := log.FromContext(ctx).WithValues("category", ev.Category, "recipient", ev.Recipient.Name, "eventKey", ev.EventKey)

	p.mu.RLock()
	settings := p.settings
	notifiers := p.notifiers
	p.mu.RUnlock()

	if !settings.categoryEnabled(ev.Category) {
		return []ChannelResult{{Outcome: OutcomeSkipped}}, nil
	}

	// Load the recipient's profile once to evaluate addresses and channel preferences.
	profile, err := p.store.GetUserProfile(ctx, ev.Recipient)
	if err != nil {
		return nil, fmt.Errorf("loading user profile: %w", err)
	}

	// Evaluate onboarding opt-out once, before the per-channel loop. An empty scope (e.g.
	// UserEnablement or pre-filtered NewServiceVersion) returns false immediately.
	suppressed, suppressReason, err := p.suppressor.Suppressed(ctx, ev.Recipient, ev.Category, ev.Scope)
	if err != nil {
		return nil, fmt.Errorf("checking opt-out: %w", err)
	}

	channels := effectiveChannels(settings, profile)

	var results []ChannelResult
	var firstErr error
	for _, ch := range channels {
		notifier, ok := notifiers[ch]
		if !ok {
			// Channel enabled in config but no notifier wired; nothing we can do.
			results = append(results, ChannelResult{Channel: ch, Outcome: OutcomeSkipped})
			continue
		}

		addr, ok := resolveAddress(ch, ev.Recipient, profile, settings)
		if !ok {
			logger.V(1).Info("no deliverable address; skipping", "channel", ch)
			results = append(results, ChannelResult{Channel: ch, Outcome: OutcomeSkipped})
			continue
		}

		res := p.deliverChannel(ctx, ev, ch, addr, notifier, suppressed, suppressReason)
		results = append(results, res)
		if res.Err != nil && firstErr == nil {
			firstErr = res.Err
		}
	}

	return results, firstErr
}

// deliverChannel handles the claim → (suppress|render→send) → record cycle for one channel.
// suppressed and suppressReason are pre-computed by Deliver before the channel loop.
func (p *Pipeline) deliverChannel(ctx context.Context, ev Event, ch v1alpha1.Channel, addr string, notifier Notifier, suppressed bool, suppressReason string) ChannelResult {
	n := store.Notification{
		Category:         ev.Category,
		Channel:          ch,
		Recipient:        ev.Recipient,
		RecipientAddress: addr,
		EventKey:         ev.EventKey,
	}

	claimed, rec, err := p.store.Claim(ctx, n)
	if err != nil {
		return ChannelResult{Channel: ch, Outcome: OutcomeFailed, Err: fmt.Errorf("claiming notification: %w", err)}
	}
	if !claimed {
		// A prior delivery already owns this notification; do not send again.
		return ChannelResult{Channel: ch, Outcome: OutcomeDuplicate}
	}

	// The claim succeeded, so this reconcile owns delivery. If an opt-out matches,
	// record a suppression so we never re-evaluate the same event.
	if suppressed {
		if err := p.store.MarkSuppressed(ctx, rec, suppressReason); err != nil {
			return ChannelResult{Channel: ch, Outcome: OutcomeFailed, Err: fmt.Errorf("recording suppression: %w", err)}
		}
		return ChannelResult{Channel: ch, Outcome: OutcomeSuppressed}
	}

	rendered, err := p.renderer.Render(ev.Category, ev.Data)
	if err != nil {
		markErr := p.store.MarkFailed(ctx, rec, fmt.Errorf("render: %w", err))
		return ChannelResult{Channel: ch, Outcome: OutcomeFailed, Err: firstNonNil(markErr, err)}
	}

	msg := Message{
		Category: ev.Category,
		Channel:  ch,
		To:       addr,
		Subject:  rendered.Subject,
		HTML:     rendered.HTML,
		Text:     rendered.Text,
	}
	if err := notifier.Send(ctx, msg); err != nil {
		if markErr := p.store.MarkFailed(ctx, rec, err); markErr != nil {
			return ChannelResult{Channel: ch, Outcome: OutcomeFailed, Err: fmt.Errorf("send failed (%v); recording failure also failed: %w", err, markErr)}
		}
		return ChannelResult{Channel: ch, Outcome: OutcomeFailed, Err: err}
	}

	if err := p.store.MarkDelivered(ctx, rec); err != nil {
		return ChannelResult{Channel: ch, Outcome: OutcomeFailed, Err: fmt.Errorf("recording delivery: %w", err)}
	}
	return ChannelResult{Channel: ch, Outcome: OutcomeDelivered}
}

// effectiveChannels returns the recipient's channels: their explicit preference if set,
// otherwise the config default.
func effectiveChannels(settings Settings, profile *v1alpha1.UserProfile) []v1alpha1.Channel {
	if profile != nil && len(profile.Spec.Preferences.Channels) > 0 {
		return profile.Spec.Preferences.Channels
	}
	return settings.EnabledChannels
}

// resolveAddress determines the channel destination for a recipient. Today only email is
// supported: an explicit UserProfile address wins, then the controller-resolved address,
// then the subject Name when usernameIsEmail is set for a User subject.
func resolveAddress(ch v1alpha1.Channel, subj v1alpha1.Subject, profile *v1alpha1.UserProfile, settings Settings) (string, bool) {
	switch ch {
	case v1alpha1.ChannelEmail:
		if profile != nil {
			if profile.Spec.Email != "" {
				return profile.Spec.Email, true
			}
			if profile.Status.ResolvedEmail != "" {
				return profile.Status.ResolvedEmail, true
			}
		}
		if settings.UsernameIsEmail && subj.Kind == v1alpha1.SubjectKindUser && subj.Name != "" {
			return subj.Name, true
		}
		return "", false
	default:
		return "", false
	}
}

func firstNonNil(errs ...error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}
