package notify

// The following structs are the strongly-typed payloads passed to templates for each
// notification category. Keeping them explicit (rather than map[string]any) makes template
// rendering type-checked and gives golden tests stable inputs.

// MembershipAddedData is the payload for a MembershipAdded notification.
type MembershipAddedData struct {
	ProductName         string
	RecipientName       string
	ResourceKind        string
	ResourceName        string
	ResourceDisplayName string
	Role                string
	Actor               string
	ConsoleURL          string
	ConnectURL          string
	SupportURL          string
}

// UserEnablementData is the payload for a first-time user-enablement notification.
type UserEnablementData struct {
	ProductName   string
	RecipientName string
	ConsoleURL    string
	DocsURL       string
	SupportURL    string
}

// AffectedControlPlane describes one control plane impacted by a new service version.
type AffectedControlPlane struct {
	Name           string
	Namespace      string
	CurrentVersion string
	// ConsoleURL is a deep link to this control plane in the platform UI, when resolvable.
	ConsoleURL string
}

// NewVersionDigestData is the payload for an aggregated new-service-version digest.
// One digest is sent per admin, covering all of that admin's affected control planes.
type NewVersionDigestData struct {
	ProductName           string
	RecipientName         string
	ServiceName           string
	NewVersion            string
	AffectedControlPlanes []AffectedControlPlane
	ConsoleURL            string
	SupportURL            string
}
