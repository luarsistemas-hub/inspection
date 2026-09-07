package notifications

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/smtp"
	"net/url"
	"strings"
	"time"
)

type SMTPSender struct{ Address, Username, Password, From string }

func (s SMTPSender) Send(ctx context.Context, intent Intent) (Receipt, error) {
	if err := ctx.Err(); err != nil {
		return Receipt{}, err
	}
	host, _, err := strings.Cut(s.Address, ":")
	if err == false || host == "" {
		return Receipt{}, errors.New("smtp: invalid address")
	}
	var auth smtp.Auth
	if s.Username != "" {
		auth = smtp.PlainAuth("", s.Username, s.Password, host)
	}
	body := []byte("To: " + intent.Destination + "\r\nFrom: " + s.From + "\r\nSubject: " + intent.Template + "\r\n\r\n" + intent.Parameters["body"])
	if err := smtp.SendMail(s.Address, auth, s.From, []string{intent.Destination}, body); err != nil {
		return Receipt{}, err
	}
	return Receipt{Provider: "smtp", ID: intent.ID, AcceptedAt: time.Now().UTC()}, nil
}

type TwilioSender struct {
	BaseURL, AccountSID, AuthToken, From string
	Client                               *http.Client
	Channel                              Channel
}

func (s TwilioSender) Send(ctx context.Context, intent Intent) (Receipt, error) {
	client := s.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	form := url.Values{"To": {intent.Destination}, "From": {s.From}, "Body": {intent.Parameters["body"]}}
	if callback, err := tenantCallbackURL(intent.Parameters["callbackUrl"], intent.Parameters["tenantId"]); err != nil {
		return Receipt{}, err
	} else if callback != "" {
		form.Set("StatusCallback", callback)
	}
	endpoint := strings.TrimRight(s.BaseURL, "/") + "/2010-04-01/Accounts/" + url.PathEscape(s.AccountSID) + "/Messages.json"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return Receipt{}, err
	}
	req.SetBasicAuth(s.AccountSID, s.AuthToken)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := client.Do(req)
	if err != nil {
		return Receipt{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return Receipt{}, fmt.Errorf("twilio: status %d", response.StatusCode)
	}
	return Receipt{Provider: "twilio", ID: response.Header.Get("X-Message-Sid"), AcceptedAt: time.Now().UTC()}, nil
}

func tenantCallbackURL(raw, tenantID string) (string, error) {
	if raw == "" && tenantID == "" {
		return "", nil
	}
	if raw == "" || tenantID == "" {
		return "", errors.New("twilio: incomplete callback scope")
	}
	callback, err := url.Parse(raw)
	if err != nil || callback.Scheme == "" || callback.Host == "" {
		return "", errors.New("twilio: invalid callback URL")
	}
	query := callback.Query()
	query.Set("tenantId", tenantID)
	callback.RawQuery = query.Encode()
	return callback.String(), nil
}
