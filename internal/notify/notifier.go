package notify

import (
	"context"

	"github.com/openmcp-project/platform-service-notifications/api/v1alpha1"
)

// Message is a fully-rendered notification ready to be delivered over a channel.
type Message struct {
	Category v1alpha1.Category
	Channel  v1alpha1.Channel
	// To is the channel-specific destination (email address, Slack handle, ...).
	To      string
	Subject string
	HTML    string
	Text    string
}

// Notifier delivers rendered messages over a single channel.
type Notifier interface {
	// Channel returns the channel this notifier delivers on.
	Channel() v1alpha1.Channel
	// Send delivers the message, returning an error if delivery could not be completed.
	Send(ctx context.Context, msg Message) error
}

// Rendered is the output of rendering an event for a channel.
type Rendered struct {
	Subject string
	HTML    string
	Text    string
}

// Renderer turns a typed event payload into a channel-ready Rendered message.
type Renderer interface {
	Render(category v1alpha1.Category, data any) (Rendered, error)
}
