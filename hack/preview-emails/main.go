// Renders all three notification email templates with sample data and writes them
// to /tmp/preview-*.html so they can be opened in a browser for visual review.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/openmcp-project/platform-service-notifications/api/v1alpha1"
	"github.com/openmcp-project/platform-service-notifications/internal/notify"
	"github.com/openmcp-project/platform-service-notifications/internal/notify/email"
)

func main() {
	r, err := email.NewRenderer()
	must("new renderer", err)

	cases := []struct {
		name     string
		category v1alpha1.Category
		data     any
	}{
		{
			name:     "welcome",
			category: v1alpha1.CategoryUserEnablement,
			data: notify.UserEnablementData{
				ProductName:   "Open Control Plane",
				RecipientName: "Jane Doe",
				ConsoleURL:    "https://app.open-control-plane.io",
				DocsURL:       "https://cloud-orchestration.github.tools.sap/docs/what-is-iad/intro",
				SupportURL:    "https://github.com/open-control-plane/support/issues",
			},
		},
		{
			name:     "membership",
			category: v1alpha1.CategoryMembershipAdded,
			data: notify.MembershipAddedData{
				ProductName:         "Open Control Plane",
				RecipientName:       "Jane Doe",
				ResourceKind:        "ControlPlane",
				ResourceName:        "poc--ws-dev--my-cp",
				ResourceDisplayName: "my-cp",
				Role:                "admin",
				Actor:               "max.mustermann@example.com",
				ConsoleURL:    "https://app.open-control-plane.io/controlplanes/poc--ws-dev--my-cp",
				ConnectURL:    "https://cloud-orchestration.github.tools.sap/docs/how-to/connect-kubectl",
				SupportURL:    "https://github.com/open-control-plane/support/issues",
			},
		},
		{
			name:     "version-digest",
			category: v1alpha1.CategoryNewServiceVersion,
			data: notify.NewVersionDigestData{
				ProductName:   "Open Control Plane",
				RecipientName: "Jane Doe",
				ServiceName:   "crossplane",
				NewVersion:    "v1.18.2",
				AffectedControlPlanes: []notify.AffectedControlPlane{
					{Name: "prod", Namespace: "project-poc--ws-prod", CurrentVersion: "v1.17.0", ConsoleURL: "https://app.open-control-plane.io/controlplanes/prod"},
					{Name: "staging", Namespace: "project-poc--ws-dev", CurrentVersion: "v1.16.3", ConsoleURL: "https://app.open-control-plane.io/controlplanes/staging"},
					{Name: "demo", Namespace: "project-poc--ws-dev", ConsoleURL: ""},
				},
				ConsoleURL: "https://app.open-control-plane.io/services/crossplane",
				SupportURL: "https://github.com/open-control-plane/support/issues",
			},
		},
	}

	outDir := "/tmp"
	for _, c := range cases {
		rendered, err := r.Render(c.category, c.data)
		must("render "+c.name, err)

		htmlPath := filepath.Join(outDir, "preview-"+c.name+".html")
		must("write "+htmlPath, os.WriteFile(htmlPath, []byte(rendered.HTML), 0o644))

		txtPath := filepath.Join(outDir, "preview-"+c.name+".txt")
		must("write "+txtPath, os.WriteFile(txtPath, []byte(rendered.Text), 0o644))

		fmt.Printf("%-20s %s\n", c.name+".html", htmlPath)
		fmt.Printf("%-20s %s\n", c.name+".txt", txtPath)
	}

	fmt.Println("\nopen in browser:")
	fmt.Println("  open /tmp/preview-welcome.html /tmp/preview-membership.html /tmp/preview-version-digest.html")
}

func must(label string, err error) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %s: %v\n", label, err)
		os.Exit(1)
	}
}
