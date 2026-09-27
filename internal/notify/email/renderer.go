// Package email renders and delivers notifications over SMTP.
package email

import (
	"bytes"
	"embed"
	"encoding/base64"
	"fmt"
	htmltemplate "html/template"
	"strings"
	texttemplate "text/template"

	"github.com/openmcp-project/platform-service-notifications/api/v1alpha1"
	"github.com/openmcp-project/platform-service-notifications/internal/notify"
)

// all: is required so that go:embed does not skip _base.html.tmpl — by default embed
// excludes files whose names begin with "_" or ".".
//
//go:embed all:templates
var templateFS embed.FS

//go:embed assets/logo.svg assets/cp-banner.jpg assets/footer-badge.png
var assetFS embed.FS

// categoryTemplates is the compiled template set for one notification category.
type categoryTemplates struct {
	subject *texttemplate.Template
	html    *htmltemplate.Template
	text    *texttemplate.Template
}

// Renderer renders notification events into email subject/HTML/text using embedded templates.
type Renderer struct {
	byCategory map[v1alpha1.Category]categoryTemplates
}

// files maps a category to its template file basenames under templates/.
var files = map[v1alpha1.Category]string{
	v1alpha1.CategoryMembershipAdded:   "membershipadded",
	v1alpha1.CategoryUserEnablement:    "userenablement",
	v1alpha1.CategoryNewServiceVersion: "newversiondigest",
}

func mustDataURI(mimeType, assetPath string) string {
	data, err := assetFS.ReadFile(assetPath)
	if err != nil {
		panic(fmt.Sprintf("email asset missing %s: %v", assetPath, err))
	}
	return "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(data)
}

// NewRenderer compiles all embedded templates. It fails fast if any template is malformed.
func NewRenderer() (*Renderer, error) {
	logoURI := mustDataURI("image/svg+xml", "assets/logo.svg")
	cpBannerURI := mustDataURI("image/jpeg", "assets/cp-banner.jpg")
	footerBadgeURI := mustDataURI("image/png", "assets/footer-badge.png")

	// Template functions expose the precomputed image data URIs without bloating template data.
	funcMap := htmltemplate.FuncMap{
		"logoDataURI":        func() htmltemplate.URL { return htmltemplate.URL(logoURI) },        //nolint:gosec
		"cpBannerDataURI":    func() htmltemplate.URL { return htmltemplate.URL(cpBannerURI) },    //nolint:gosec
		"footerBadgeDataURI": func() htmltemplate.URL { return htmltemplate.URL(footerBadgeURI) }, //nolint:gosec
	}

	r := &Renderer{byCategory: make(map[v1alpha1.Category]categoryTemplates, len(files))}
	for cat, base := range files {
		htmlT, err := htmltemplate.New("").Funcs(funcMap).ParseFS(templateFS,
			"templates/_base.html.tmpl",
			"templates/"+base+".html.tmpl",
		)
		if err != nil {
			return nil, err
		}
		subjectT, err := texttemplate.ParseFS(templateFS, "templates/"+base+".subject.tmpl")
		if err != nil {
			return nil, err
		}
		textT, err := texttemplate.ParseFS(templateFS, "templates/"+base+".txt.tmpl")
		if err != nil {
			return nil, err
		}
		r.byCategory[cat] = categoryTemplates{
			subject: subjectT,
			html:    htmlT,
			text:    textT,
		}
	}
	return r, nil
}

var _ notify.Renderer = (*Renderer)(nil)

// Render produces the email subject, HTML body, and plaintext body for the given category.
func (r *Renderer) Render(category v1alpha1.Category, data any) (notify.Rendered, error) {
	ct, ok := r.byCategory[category]
	if !ok {
		return notify.Rendered{}, &UnknownCategoryError{Category: category}
	}

	var subj, html, text bytes.Buffer
	if err := ct.subject.Execute(&subj, data); err != nil {
		return notify.Rendered{}, err
	}
	if err := ct.html.ExecuteTemplate(&html, "layout", data); err != nil {
		return notify.Rendered{}, err
	}
	if err := ct.text.Execute(&text, data); err != nil {
		return notify.Rendered{}, err
	}
	return notify.Rendered{
		Subject: strings.TrimSpace(subj.String()),
		HTML:    html.String(),
		Text:    text.String(),
	}, nil
}

// UnknownCategoryError is returned when a category has no registered template.
type UnknownCategoryError struct{ Category v1alpha1.Category }

func (e *UnknownCategoryError) Error() string {
	return "no email template registered for category " + string(e.Category)
}
