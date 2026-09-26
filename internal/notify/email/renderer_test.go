package email

import (
	"strings"
	"testing"

	"github.com/openmcp-project/platform-service-notifications/api/v1alpha1"
	"github.com/openmcp-project/platform-service-notifications/internal/notify"
)

const testRoleAdmin = "admin"

func newRenderer(t *testing.T) *Renderer {
	t.Helper()
	r, err := NewRenderer()
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}
	return r
}

func TestRender_MembershipAdded(t *testing.T) {
	r := newRenderer(t)
	out, err := r.Render(v1alpha1.CategoryMembershipAdded, notify.MembershipAddedData{
		RecipientName:       "alice@x.io",
		ResourceKind:        "Project",
		ResourceName:        "team-a",
		ResourceDisplayName: "Team A",
		Role:                testRoleAdmin,
		ConsoleURL:          "https://console/project/ns/team-a",
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.TrimSpace(out.Subject) == "" {
		t.Error("subject must not be empty")
	}
	for _, want := range []string{"Team A", testRoleAdmin} {
		if !strings.Contains(out.HTML, want) {
			t.Errorf("HTML missing %q:\n%s", want, out.HTML)
		}
		if !strings.Contains(out.Text, want) {
			t.Errorf("Text missing %q:\n%s", want, out.Text)
		}
	}
	if !strings.Contains(out.HTML, "https://console/project/ns/team-a") {
		t.Errorf("HTML missing console link:\n%s", out.HTML)
	}
}

func TestRender_UserEnablement(t *testing.T) {
	r := newRenderer(t)
	out, err := r.Render(v1alpha1.CategoryUserEnablement, notify.UserEnablementData{
		ProductName:   "Acme Cloud",
		RecipientName: "bob@x.io",
		ConsoleURL:    "https://acme.eu",
		DocsURL:       "https://acme.eu/help/",
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if out.Subject == "" || out.HTML == "" || out.Text == "" {
		t.Fatalf("all parts must be populated: %+v", out)
	}
	// The product name must be configurable end to end (subject + body).
	if !strings.Contains(out.Subject, "Welcome to Acme Cloud") {
		t.Errorf("subject should greet with product name, got %q", out.Subject)
	}
	for _, part := range []string{out.HTML, out.Text} {
		if !strings.Contains(part, "Welcome to Acme Cloud") {
			t.Errorf("body missing product-name greeting:\n%s", part)
		}
	}
	if !strings.Contains(out.HTML, "https://acme.eu/help/") {
		t.Errorf("enablement HTML missing docs link:\n%s", out.HTML)
	}
}

func TestRender_ProductNameFallback(t *testing.T) {
	r := newRenderer(t)
	// With no ProductName set, templates fall back to the default.
	out, err := r.Render(v1alpha1.CategoryUserEnablement, notify.UserEnablementData{RecipientName: "x"})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(out.Subject, "Welcome to Open Control Plane") {
		t.Errorf("expected default product name fallback, got subject %q", out.Subject)
	}
}

func TestRender_NewVersionDigest_AggregatesControlPlanes(t *testing.T) {
	r := newRenderer(t)
	out, err := r.Render(v1alpha1.CategoryNewServiceVersion, notify.NewVersionDigestData{
		RecipientName: "admin@x.io",
		ServiceName:   "crossplane",
		NewVersion:    "v1.2.3",
		AffectedControlPlanes: []notify.AffectedControlPlane{
			{Name: "cp-one", Namespace: "ns1"},
			{Name: "cp-two", Namespace: "ns2"},
		},
		ConsoleURL: "https://console",
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	// A single digest must list every affected control plane.
	for _, want := range []string{"crossplane", "v1.2.3", "cp-one", "cp-two"} {
		if !strings.Contains(out.HTML, want) {
			t.Errorf("digest HTML missing %q:\n%s", want, out.HTML)
		}
		if !strings.Contains(out.Text, want) {
			t.Errorf("digest Text missing %q:\n%s", want, out.Text)
		}
	}
}

func TestRender_HTMLEscaping(t *testing.T) {
	r := newRenderer(t)
	out, err := r.Render(v1alpha1.CategoryMembershipAdded, notify.MembershipAddedData{
		RecipientName:       "alice@x.io",
		ResourceKind:        "Project",
		ResourceName:        "p",
		ResourceDisplayName: "<script>alert(1)</script>",
		Role:                testRoleAdmin,
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.Contains(out.HTML, "<script>alert(1)</script>") {
		t.Errorf("html/template must escape injected markup:\n%s", out.HTML)
	}
	if !strings.Contains(out.HTML, "&lt;script&gt;") {
		t.Errorf("expected escaped script tag in HTML:\n%s", out.HTML)
	}
}

func TestRender_UnknownCategory(t *testing.T) {
	r := newRenderer(t)
	_, err := r.Render(v1alpha1.Category("Bogus"), nil)
	if err == nil {
		t.Fatal("expected error for unknown category")
	}
	if _, ok := err.(*UnknownCategoryError); !ok {
		t.Errorf("expected *UnknownCategoryError, got %T", err)
	}
}
