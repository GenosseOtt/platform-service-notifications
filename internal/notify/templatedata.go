package notify

// The following structs are the strongly-typed payloads passed to templates for each
// notification category. Keeping them explicit (rather than map[string]any) makes template
// rendering type-checked and gives golden tests stable inputs.

// MembershipAddedData is the payload for a MembershipAdded notification.
type MembershipAddedData struct {
	// ProductName is the platform/product name shown in the layout.
	ProductName string
	// RecipientName is a display name for the greeting (falls back to the address).
	RecipientName string
	// ResourceKind is "Project", "Workspace", or "ControlPlane".
	ResourceKind string
	// ResourceName is the name of the resource the recipient was added to.
	ResourceName string
	// ResourceDisplayName is the human-friendly name, when available.
	ResourceDisplayName string
	// Role is the role the recipient was granted (e.g. "admin", "view").
	Role string
	// Actor is the identity that performed the change, when known.
	Actor string
	// ConsoleURL is a deep link to the resource.
	ConsoleURL string
}

// UserEnablementData is the payload for a first-time user-enablement notification.
type UserEnablementData struct {
	// ProductName is the platform/product name shown in the greeting ("Welcome to <ProductName>").
	ProductName   string
	RecipientName string
	// ConsoleURL is the platform console entry point.
	ConsoleURL string
	// DocsURL is the getting-started documentation link.
	DocsURL string
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
	// ProductName is the platform/product name shown in the layout.
	ProductName   string
	RecipientName string
	// ServiceName is the service that has a newer version.
	ServiceName string
	// NewVersion is the newly available version.
	NewVersion string
	// AffectedControlPlanes are the recipient's control planes using the service.
	AffectedControlPlanes []AffectedControlPlane
	// ConsoleURL is a deep link to the service catalog / upgrade view.
	ConsoleURL string
}
