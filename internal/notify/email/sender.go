package email

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/wneessen/go-mail"

	"github.com/openmcp-project/platform-service-notifications/api/v1alpha1"
	"github.com/openmcp-project/platform-service-notifications/internal/notify"
)

// Config is the resolved SMTP configuration needed to send mail. Connection details come
// from NotificationConfig; Username/Password are resolved from the referenced Secret.
type Config struct {
	Host          string
	Port          int
	StartTLS      bool
	Username      string
	Password      string
	SenderAddress string
	SenderName    string
	ReplyTo       string
}

func (c Config) validate() error {
	if c.Host == "" {
		return fmt.Errorf("smtp host is not configured")
	}
	if c.SenderAddress == "" {
		return fmt.Errorf("smtp sender address is not configured")
	}
	return nil
}

// Sender delivers messages over SMTP. Its configuration can be updated at runtime
// (e.g. when the credentials Secret changes) via SetConfig.
type Sender struct {
	mu  sync.RWMutex
	cfg Config
}

// NewSender returns a Sender with the given initial configuration.
func NewSender(cfg Config) *Sender {
	return &Sender{cfg: cfg}
}

// SetConfig atomically replaces the sender configuration.
func (s *Sender) SetConfig(cfg Config) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cfg = cfg
}

var _ notify.Notifier = (*Sender)(nil)

// Channel implements notify.Notifier.
func (s *Sender) Channel() v1alpha1.Channel { return v1alpha1.ChannelEmail }

// Send builds a multipart/alternative message (plaintext + HTML) and delivers it.
func (s *Sender) Send(ctx context.Context, m notify.Message) error {
	s.mu.RLock()
	cfg := s.cfg
	s.mu.RUnlock()

	if err := cfg.validate(); err != nil {
		return err
	}
	if m.To == "" {
		return fmt.Errorf("message has no recipient address")
	}

	client, err := s.newClient(cfg)
	if err != nil {
		return fmt.Errorf("building smtp client: %w", err)
	}

	msg := mail.NewMsg()
	if cfg.SenderName != "" {
		if err := msg.FromFormat(cfg.SenderName, cfg.SenderAddress); err != nil {
			return fmt.Errorf("setting from: %w", err)
		}
	} else if err := msg.From(cfg.SenderAddress); err != nil {
		return fmt.Errorf("setting from: %w", err)
	}
	if err := msg.To(m.To); err != nil {
		return fmt.Errorf("setting recipient: %w", err)
	}
	if cfg.ReplyTo != "" {
		if err := msg.ReplyTo(cfg.ReplyTo); err != nil {
			return fmt.Errorf("setting reply-to: %w", err)
		}
	}
	msg.Subject(m.Subject)
	// Plaintext is the primary body; HTML is the richer alternative.
	msg.SetBodyString(mail.TypeTextPlain, m.Text)
	if m.HTML != "" {
		msg.AddAlternativeString(mail.TypeTextHTML, m.HTML)
	}

	if err := client.DialAndSendWithContext(ctx, msg); err != nil {
		return fmt.Errorf("sending email: %w", err)
	}
	return nil
}

func (s *Sender) newClient(cfg Config) (*mail.Client, error) {
	port := cfg.Port
	if port == 0 {
		port = 587
	}
	opts := []mail.Option{
		mail.WithPort(port),
		mail.WithTimeout(30 * time.Second),
	}
	if cfg.StartTLS {
		opts = append(opts, mail.WithTLSPolicy(mail.TLSMandatory))
	} else {
		opts = append(opts, mail.WithTLSPolicy(mail.NoTLS))
	}
	if cfg.Username != "" {
		opts = append(opts,
			mail.WithSMTPAuth(mail.SMTPAuthPlain),
			mail.WithUsername(cfg.Username),
			mail.WithPassword(cfg.Password),
		)
	}
	return mail.NewClient(cfg.Host, opts...)
}
