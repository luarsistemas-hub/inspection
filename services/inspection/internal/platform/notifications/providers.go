package notifications

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// SMTPSender sends multipart operational email. SMTP acknowledgement means
// accepted, never delivered: SMTP has no portable delivery callback.
type SMTPSender struct {
	Address, Username, Password, From, ReplyTo, TLSMode string
	Timeout                                             time.Duration
}

func (s SMTPSender) Send(ctx context.Context, intent Intent) (Receipt, error) {
	return s.send(ctx, DeliveryIntent{ID: intent.ID, Destination: intent.Destination, Template: intent.Template, Subject: intent.Template, Text: intent.Parameters["body"], HTML: intent.Parameters["html"]})
}

func (s SMTPSender) send(ctx context.Context, intent DeliveryIntent) (Receipt, error) {
	if err := validateMailHeaders(s.From, s.ReplyTo, intent.Destination, intent.Subject); err != nil {
		return Receipt{}, err
	}
	host, _, err := net.SplitHostPort(s.Address)
	if err != nil || host == "" {
		return Receipt{}, &DeliveryError{Kind: ErrorPermanent, Code: "smtp_invalid_address", PreSend: true}
	}
	if err := ctx.Err(); err != nil {
		return Receipt{}, &DeliveryError{Kind: ErrorCanceled, Code: "canceled", PreSend: true}
	}
	mode := s.TLSMode
	if mode == "" {
		mode = "none"
	}
	if mode != "none" && mode != "starttls" && mode != "tls" {
		return Receipt{}, &DeliveryError{Kind: ErrorPermanent, Code: "smtp_invalid_tls_mode", PreSend: true}
	}
	message, err := buildMIMEMessage(s.From, s.ReplyTo, intent.Destination, intent.Subject, intent.Text, intent.HTML)
	if err != nil {
		return Receipt{}, err
	}
	timeout := s.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	conn, err := (&net.Dialer{Timeout: timeout}).DialContext(ctx, "tcp", s.Address)
	if err != nil {
		return Receipt{}, smtpConnectionError(ctx, true)
	}
	defer conn.Close()
	stopCancel := closeOnCancel(ctx, conn)
	defer stopCancel()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	} else {
		_ = conn.SetDeadline(time.Now().Add(timeout))
	}
	if mode == "tls" {
		tlsConn := tls.Client(conn, &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12})
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			return Receipt{}, smtpConnectionError(ctx, true)
		}
		conn = tlsConn
	}
	client, err := smtp.NewClient(conn, host)
	if err != nil {
		return Receipt{}, smtpConnectionError(ctx, true)
	}
	defer client.Close()
	if mode == "starttls" {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return Receipt{}, &DeliveryError{Kind: ErrorPermanent, Code: "smtp_starttls_unavailable", PreSend: true}
		}
		if err := client.StartTLS(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}); err != nil {
			return Receipt{}, smtpConnectionError(ctx, true)
		}
	}
	if s.Username != "" {
		if err := client.Auth(smtp.PlainAuth("", s.Username, s.Password, host)); err != nil {
			return Receipt{}, &DeliveryError{Kind: ErrorPermanent, Code: "smtp_auth_failed", PreSend: true}
		}
	}
	if err := client.Mail(addressOnly(s.From)); err != nil {
		return Receipt{}, smtpConnectionError(ctx, true)
	}
	if err := client.Rcpt(addressOnly(intent.Destination)); err != nil {
		return Receipt{}, &DeliveryError{Kind: ErrorPermanent, Code: "smtp_recipient_rejected", PreSend: true}
	}
	writer, err := client.Data()
	if err != nil {
		return Receipt{}, smtpConnectionError(ctx, true)
	}
	if _, err = writer.Write(message); err != nil {
		_ = writer.Close()
		return Receipt{}, smtpConnectionError(ctx, false)
	}
	if err = writer.Close(); err != nil {
		return Receipt{}, smtpConnectionError(ctx, false)
	}
	return Receipt{Provider: string(ProviderSMTP), ID: intent.ID, AcceptedAt: time.Now().UTC()}, nil
}

type smtpAdapter struct{ SMTPSender }

func (s smtpAdapter) Send(ctx context.Context, intent DeliveryIntent) (Receipt, error) {
	return s.send(ctx, intent)
}

var _ Adapter = smtpAdapter{}

// SMTPAdapter adapts an SMTP sender to the provider-neutral Gateway.
func SMTPAdapter(sender SMTPSender) Adapter { return smtpAdapter{sender} }

// TwilioSMSSender is the Twilio Messages adapter for the SMS channel only.
type TwilioSMSSender struct {
	BaseURL, AccountSID, AuthToken, From, StatusCallback string
	Client                                               *http.Client
}

func (s TwilioSMSSender) Send(ctx context.Context, intent DeliveryIntent) (Receipt, error) {
	form := url.Values{"To": {intent.Destination}, "From": {s.From}, "Body": {intent.Text}}
	callback := s.StatusCallback
	if intent.CallbackURL != "" {
		callback = intent.CallbackURL
	}
	if callback, err := tenantCallbackURL(callback, intent.TenantID); err != nil {
		return Receipt{}, &DeliveryError{Kind: ErrorPermanent, Code: "twilio_invalid_callback", PreSend: true}
	} else if callback != "" {
		form.Set("StatusCallback", callback)
	}
	return s.post(ctx, form)
}

func (s TwilioSMSSender) post(ctx context.Context, form url.Values) (Receipt, error) {
	endpoint := strings.TrimRight(s.BaseURL, "/") + "/2010-04-01/Accounts/" + url.PathEscape(s.AccountSID) + "/Messages.json"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return Receipt{}, &DeliveryError{Kind: ErrorPermanent, Code: "twilio_invalid_request", PreSend: true}
	}
	req.SetBasicAuth(s.AccountSID, s.AuthToken)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := s.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	response, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return Receipt{}, &DeliveryError{Kind: ErrorUnknown, Code: "twilio_interrupted"}
		}
		return Receipt{}, &DeliveryError{Kind: ErrorTransient, Code: "twilio_unavailable", PreSend: true}
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return Receipt{}, classifyHTTPError("twilio", response.StatusCode, response.Header.Get("Retry-After"))
	}
	var payload struct {
		SID string `json:"sid"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 64*1024)).Decode(&payload); err != nil || payload.SID == "" {
		return Receipt{}, &DeliveryError{Kind: ErrorUnknown, Code: "twilio_invalid_receipt"}
	}
	return Receipt{Provider: string(ProviderTwilio), ID: payload.SID, AcceptedAt: time.Now().UTC()}, nil
}

// TwilioWhatsAppSender sends only approved Twilio Content API templates.
type TwilioWhatsAppSender struct {
	TwilioSMSSender
	ContentSIDs map[string]string
}

func (s TwilioWhatsAppSender) Send(ctx context.Context, intent DeliveryIntent) (Receipt, error) {
	contentSID := s.ContentSIDs[templateKey(intent.Template, intent.Language)]
	if contentSID == "" {
		return Receipt{}, &DeliveryError{Kind: ErrorPermanent, Code: "twilio_template_unavailable", PreSend: true}
	}
	variables, err := json.Marshal(orderedVariables(intent.TemplateParameters))
	if err != nil {
		return Receipt{}, &DeliveryError{Kind: ErrorPermanent, Code: "twilio_invalid_template", PreSend: true}
	}
	form := url.Values{"To": {whatsAppAddress(intent.Destination)}, "From": {whatsAppAddress(s.From)}, "ContentSid": {contentSID}, "ContentVariables": {string(variables)}}
	callback := s.StatusCallback
	if intent.CallbackURL != "" {
		callback = intent.CallbackURL
	}
	if callback, err := tenantCallbackURL(callback, intent.TenantID); err != nil {
		return Receipt{}, &DeliveryError{Kind: ErrorPermanent, Code: "twilio_invalid_callback", PreSend: true}
	} else if callback != "" {
		form.Set("StatusCallback", callback)
	}
	return s.post(ctx, form)
}

// MetaWhatsAppSender is the versioned Meta Cloud API template adapter.
type MetaWhatsAppSender struct {
	BaseURL, APIVersion, PhoneNumberID, AccessToken string
	Templates                                       map[string]string
	Client                                          *http.Client
}

func (s MetaWhatsAppSender) Send(ctx context.Context, intent DeliveryIntent) (Receipt, error) {
	name := s.Templates[templateKey(intent.Template, intent.Language)]
	if name == "" {
		return Receipt{}, &DeliveryError{Kind: ErrorPermanent, Code: "meta_template_unavailable", PreSend: true}
	}
	parameters := make([]map[string]string, 0, len(intent.TemplateParameters))
	for _, value := range intent.TemplateParameters {
		parameters = append(parameters, map[string]string{"type": "text", "text": value})
	}
	payload := map[string]any{"messaging_product": "whatsapp", "to": strings.TrimPrefix(intent.Destination, "+"), "type": "template", "template": map[string]any{"name": name, "language": map[string]string{"code": strings.ReplaceAll(intent.Language, "-", "_")}, "components": []map[string]any{{"type": "body", "parameters": parameters}}}}
	body, err := json.Marshal(payload)
	if err != nil {
		return Receipt{}, &DeliveryError{Kind: ErrorPermanent, Code: "meta_invalid_template", PreSend: true}
	}
	baseURL := s.BaseURL
	if baseURL == "" {
		baseURL = "https://graph.facebook.com"
	}
	endpoint := strings.TrimRight(baseURL, "/") + "/" + url.PathEscape(s.APIVersion) + "/" + url.PathEscape(s.PhoneNumberID) + "/messages"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(body)))
	if err != nil {
		return Receipt{}, &DeliveryError{Kind: ErrorPermanent, Code: "meta_invalid_request", PreSend: true}
	}
	req.Header.Set("Authorization", "Bearer "+s.AccessToken)
	req.Header.Set("Content-Type", "application/json")
	client := s.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	response, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return Receipt{}, &DeliveryError{Kind: ErrorUnknown, Code: "meta_interrupted"}
		}
		return Receipt{}, &DeliveryError{Kind: ErrorTransient, Code: "meta_unavailable", PreSend: true}
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return Receipt{}, classifyHTTPError("meta", response.StatusCode, response.Header.Get("Retry-After"))
	}
	var result struct {
		Messages []struct {
			ID string `json:"id"`
		} `json:"messages"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 64*1024)).Decode(&result); err != nil || len(result.Messages) == 0 || result.Messages[0].ID == "" {
		return Receipt{}, &DeliveryError{Kind: ErrorUnknown, Code: "meta_invalid_receipt"}
	}
	return Receipt{Provider: string(ProviderMeta), ID: result.Messages[0].ID, AcceptedAt: time.Now().UTC()}, nil
}

// TwilioSender remains the legacy authentication sender. New operational work
// uses TwilioSMSSender or TwilioWhatsAppSender through Gateway.
type TwilioSender struct {
	BaseURL, AccountSID, AuthToken, From string
	Client                               *http.Client
	Channel                              Channel
}

func (s TwilioSender) Send(ctx context.Context, intent Intent) (Receipt, error) {
	return (TwilioSMSSender{BaseURL: s.BaseURL, AccountSID: s.AccountSID, AuthToken: s.AuthToken, From: s.From, Client: s.Client}).Send(ctx, DeliveryIntent{ID: intent.ID, Destination: intent.Destination, Text: intent.Parameters["body"], CallbackURL: intent.Parameters["callbackUrl"], TenantID: intent.Parameters["tenantId"]})
}

func templateKey(template, language string) string { return template + ":" + language }
func whatsAppAddress(value string) string {
	if strings.HasPrefix(value, "whatsapp:") {
		return value
	}
	return "whatsapp:" + value
}
func orderedVariables(values []string) map[string]string {
	result := make(map[string]string, len(values))
	for i, value := range values {
		result[strconv.Itoa(i+1)] = value
	}
	return result
}
func classifyHTTPError(provider string, status int, retryAfter string) error {
	if status == http.StatusTooManyRequests || status >= 500 {
		return &DeliveryError{Kind: ErrorTransient, Code: provider + "_unavailable", RetryAfter: parseRetryAfter(retryAfter), PreSend: true}
	}
	return &DeliveryError{Kind: ErrorPermanent, Code: provider + "_rejected", PreSend: true}
}
func parseRetryAfter(value string) time.Duration {
	seconds, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || seconds <= 0 {
		return 0
	}
	return time.Duration(seconds) * time.Second
}

func tenantCallbackURL(raw, tenantID string) (string, error) {
	if raw == "" && tenantID == "" {
		return "", nil
	}
	if raw == "" || tenantID == "" {
		return "", errors.New("incomplete callback scope")
	}
	callback, err := url.Parse(raw)
	if err != nil || callback.Scheme == "" || callback.Host == "" {
		return "", errors.New("invalid callback URL")
	}
	query := callback.Query()
	query.Set("tenantId", tenantID)
	callback.RawQuery = query.Encode()
	return callback.String(), nil
}
func validateMailHeaders(values ...string) error {
	for _, value := range values {
		if strings.ContainsAny(value, "\r\n") {
			return &DeliveryError{Kind: ErrorPermanent, Code: "smtp_invalid_header", PreSend: true}
		}
	}
	return nil
}
func buildMIMEMessage(from, replyTo, destination, subject, text, html string) ([]byte, error) {
	if text == "" {
		text = html
	}
	if html == "" {
		html = text
	}
	var body strings.Builder
	boundary := "inspection-notification-boundary"
	body.WriteString("From: " + from + "\r\nTo: " + destination + "\r\n")
	if replyTo != "" {
		body.WriteString("Reply-To: " + replyTo + "\r\n")
	}
	body.WriteString("Subject: " + mime.QEncoding.Encode("UTF-8", subject) + "\r\nMIME-Version: 1.0\r\nContent-Type: multipart/alternative; boundary=\"" + boundary + "\"\r\n\r\n")
	writer := multipart.NewWriter(&body)
	if err := writer.SetBoundary(boundary); err != nil {
		return nil, err
	}
	for _, part := range []struct{ contentType, value string }{{"text/plain; charset=UTF-8", text}, {"text/html; charset=UTF-8", html}} {
		header := textproto.MIMEHeader{"Content-Type": {part.contentType}, "Content-Transfer-Encoding": {"8bit"}}
		partWriter, err := writer.CreatePart(header)
		if err != nil {
			return nil, err
		}
		if _, err = io.WriteString(partWriter, part.value); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return []byte(body.String()), nil
}
func addressOnly(raw string) string {
	address, err := mail.ParseAddress(raw)
	if err == nil {
		return address.Address
	}
	return raw
}
func closeOnCancel(ctx context.Context, conn net.Conn) func() {
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-done:
		}
	}()
	return func() { close(done) }
}
func smtpConnectionError(ctx context.Context, preSend bool) error {
	if ctx.Err() != nil {
		return &DeliveryError{Kind: ErrorUnknown, Code: "smtp_interrupted", PreSend: preSend}
	}
	if !preSend {
		return &DeliveryError{Kind: ErrorUnknown, Code: "smtp_unconfirmed", PreSend: false}
	}
	return &DeliveryError{Kind: ErrorTransient, Code: "smtp_unavailable", PreSend: preSend}
}
