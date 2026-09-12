package core

import (
	"fmt"
	"sort"
	"strings"
)

// RenderedTemplate is provider-neutral content ready for a channel adapter.
type RenderedTemplate struct {
	Subject    string
	Text       string
	HTML       string
	Parameters []string
}

// Template defines one logical, versioned template and its variable schema.
type Template struct {
	Ref      TemplateRef
	Channels map[Channel]struct{}
	Required []string
	render   func(map[string]string) RenderedTemplate
}

// ValidateVariables checks declared variables before any rendered body exists.
func (t Template) ValidateVariables(variables map[string]string) error {
	required := make(map[string]struct{}, len(t.Required))
	for _, name := range t.Required {
		required[name] = struct{}{}
		if strings.TrimSpace(variables[name]) == "" {
			return &Error{Code: MissingVariable, Field: name}
		}
	}
	for name := range variables {
		if _, ok := required[name]; !ok {
			return &Error{Code: IncompatibleVariables, Field: name}
		}
	}
	return nil
}

// Render validates then renders a versioned logical template.
func (t Template) Render(variables map[string]string) (RenderedTemplate, error) {
	if err := t.ValidateVariables(variables); err != nil {
		return RenderedTemplate{}, err
	}
	return t.render(variables), nil
}

// Catalog is an immutable lookup of logical templates.
type Catalog struct{ templates map[string]Template }

// DefaultCatalog contains the operational notification families owned by this workflow.
func DefaultCatalog() Catalog {
	all := map[Channel]struct{}{ChannelEmail: {}, ChannelSMS: {}, ChannelWhatsApp: {}}
	return Catalog{templates: map[string]Template{
		"capture-link:v1": {Ref: TemplateRef{Name: "capture-link", Version: "v1"}, Channels: all, Required: []string{"captureUrl", "recipientName"}, render: func(v map[string]string) RenderedTemplate {
			return RenderedTemplate{Subject: "Sua inspeção está disponível", Text: "Olá " + v["recipientName"] + ", acesse " + v["captureUrl"], HTML: "<p>Olá " + v["recipientName"] + ", acesse <a href=\"" + v["captureUrl"] + "\">sua inspeção</a>.</p>", Parameters: []string{v["captureUrl"], v["recipientName"]}}
		}},
		"recapture-link:v1": {Ref: TemplateRef{Name: "recapture-link", Version: "v1"}, Channels: all, Required: []string{"recaptureUrl", "recipientName"}, render: func(v map[string]string) RenderedTemplate {
			return RenderedTemplate{Subject: "Recaptura solicitada", Text: "Olá " + v["recipientName"] + ", acesse " + v["recaptureUrl"], HTML: "<p>Olá " + v["recipientName"] + ", acesse <a href=\"" + v["recaptureUrl"] + "\">a recaptura</a>.</p>", Parameters: []string{v["recaptureUrl"], v["recipientName"]}}
		}},
		"critical-alert:v1": {Ref: TemplateRef{Name: "critical-alert", Version: "v1"}, Channels: all, Required: []string{"inspectionName", "dashboardUrl"}, render: func(v map[string]string) RenderedTemplate {
			return RenderedTemplate{Subject: "Alerta crítico", Text: "A inspeção " + v["inspectionName"] + " requer revisão: " + v["dashboardUrl"], HTML: "<p>A inspeção " + v["inspectionName"] + " requer <a href=\"" + v["dashboardUrl"] + "\">revisão</a>.</p>", Parameters: []string{v["inspectionName"], v["dashboardUrl"]}}
		}},
		"reminder:v1": {Ref: TemplateRef{Name: "reminder", Version: "v1"}, Channels: all, Required: []string{"captureUrl", "recipientName"}, render: func(v map[string]string) RenderedTemplate {
			return RenderedTemplate{Subject: "Lembrete de inspeção", Text: "Olá " + v["recipientName"] + ", lembrete: " + v["captureUrl"], HTML: "<p>Olá " + v["recipientName"] + ", <a href=\"" + v["captureUrl"] + "\">conclua sua inspeção</a>.</p>", Parameters: []string{v["captureUrl"], v["recipientName"]}}
		}},
	}}
}

// Resolve returns an exact template revision enabled for channel.
func (c Catalog) Resolve(ref TemplateRef, channel Channel) (Template, error) {
	template, ok := c.templates[ref.String()]
	if !ok {
		return Template{}, &Error{Code: UnknownTemplate, Field: "template"}
	}
	if _, ok := template.Channels[channel]; !ok {
		return Template{}, &Error{Code: UnknownChannel, Field: "channel"}
	}
	return template, nil
}

// Keys lists catalog revisions in stable order for configuration validation.
func (c Catalog) Keys() []string {
	keys := make([]string, 0, len(c.templates))
	for key := range c.templates {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// ParseTemplateRef parses the catalog's name:vN key format.
func ParseTemplateRef(raw string) (TemplateRef, error) {
	parts := strings.Split(raw, ":")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return TemplateRef{}, fmt.Errorf("invalid template reference")
	}
	return TemplateRef{Name: parts[0], Version: parts[1]}, nil
}
