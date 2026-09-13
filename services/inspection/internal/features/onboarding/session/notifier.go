package session

import (
	"context"
	"errors"
	"fmt"

	"inspection/services/inspection/internal/platform/notifications"
)

type RegistryNotifier struct{ Registry *notifications.Registry }

func (n RegistryNotifier) SendOTP(ctx context.Context, destination, code string) error {
	if n.Registry == nil {
		return errors.New("onboarding notifier: missing registry")
	}
	text, html := otpEmailContent(code)
	result := notifications.Deliver(ctx, n.Registry, map[notifications.Channel]notifications.Intent{
		notifications.Email: {
			ID:          "onboarding:" + destination,
			Destination: destination,
			Template:    "Seu código de confirmação | Inspection",
			Parameters:  map[string]string{"body": text, "html": html},
		},
	})
	if result.Status != "DELIVERED" {
		return errors.New("onboarding notifier: delivery failed")
	}
	return nil
}

func otpEmailContent(code string) (string, string) {
	text := fmt.Sprintf("Olá!\n\nSeu código de confirmação do Inspection é: %s\n\nEle é válido por alguns minutos e só pode ser usado uma vez. Se você não solicitou este código, ignore este e-mail.\n\nInspection · Imobiliária", code)
	html := fmt.Sprintf(`<!doctype html>
<html lang="pt-BR">
<body style="margin:0;background:#eef8f7;color:#102f32;font-family:Arial,Helvetica,sans-serif;">
  <table role="presentation" width="100%%" cellspacing="0" cellpadding="0" style="background:#eef8f7;padding:32px 16px;">
    <tr>
      <td align="center">
        <table role="presentation" width="100%%" cellspacing="0" cellpadding="0" style="max-width:560px;background:#ffffff;border:1px solid #c5e0de;border-radius:16px;">
          <tr>
            <td style="padding:32px 36px 12px;">
              <p style="margin:0;color:#527477;font-size:16px;font-weight:700;letter-spacing:.02em;">Inspection · Imobiliária</p>
            </td>
          </tr>
          <tr>
            <td style="padding:12px 36px 36px;">
              <h1 style="margin:0 0 16px;color:#102f32;font-size:28px;line-height:1.2;">Seu código de confirmação</h1>
              <p style="margin:0 0 24px;color:#527477;font-size:16px;line-height:1.6;">Use o código abaixo para confirmar seu acesso ao Inspection:</p>
              <p style="margin:0 0 24px;background:#eef8f7;border:1px solid #c5e0de;border-radius:12px;color:#087f78;font-size:36px;font-weight:700;letter-spacing:10px;line-height:1;padding:24px 16px;text-align:center;">%s</p>
              <p style="margin:0;color:#527477;font-size:14px;line-height:1.6;">O código é válido por alguns minutos e só pode ser usado uma vez.</p>
              <p style="margin:16px 0 0;color:#527477;font-size:14px;line-height:1.6;">Se você não solicitou este código, ignore este e-mail.</p>
            </td>
          </tr>
        </table>
        <p style="margin:16px 0 0;color:#527477;font-size:12px;line-height:1.5;">Este é um e-mail automático. Não é necessário responder.</p>
      </td>
    </tr>
  </table>
</body>
</html>`, code)
	return text, html
}
