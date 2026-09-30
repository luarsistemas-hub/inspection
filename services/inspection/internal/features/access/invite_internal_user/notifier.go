package invite_internal_user

import (
	"context"
	"errors"
	"fmt"
	"html"

	"inspection/services/inspection/internal/platform/notifications"
)

type RegistryNotifier struct{ Registry *notifications.Registry }

func (n RegistryNotifier) SendInvitation(ctx context.Context, destination, link string, membershipID, generation string) error {
	if n.Registry == nil {
		return errors.New("user invitation: missing notification registry")
	}
	safeLink := html.EscapeString(link)
	text := fmt.Sprintf("Você recebeu um convite para acessar o Inspection. Acesse o link para confirmar seu e-mail e ativar o acesso: %s\n\nSe você não esperava este convite, ignore esta mensagem.", link)
	body := fmt.Sprintf(`<!doctype html><html lang="pt-BR"><body><h1>Convite para acessar o Inspection</h1><p>Confirme seu e-mail para ativar o acesso.</p><p><a href="%s">Aceitar convite</a></p><p>Se você não esperava este convite, ignore esta mensagem.</p></body></html>`, safeLink)
	return deliver(ctx, n.Registry, notifications.Intent{ID: "access-invitation:" + membershipID + ":" + generation, Destination: destination, Template: "Ative seu acesso ao Inspection", Parameters: map[string]string{"body": text, "html": body}})
}

func (n RegistryNotifier) SendOTP(ctx context.Context, destination, code, challengeID string) error {
	if n.Registry == nil {
		return errors.New("user invitation: missing notification registry")
	}
	text := fmt.Sprintf("Seu código de ativação do Inspection é %s. Ele é válido por alguns minutos e só pode ser usado uma vez.", code)
	body := fmt.Sprintf(`<!doctype html><html lang="pt-BR"><body><h1>Seu código de ativação</h1><p>Use este código para confirmar o acesso:</p><p style="font-size:32px;font-weight:bold;letter-spacing:8px">%s</p><p>O código é válido por alguns minutos.</p></body></html>`, html.EscapeString(code))
	return deliver(ctx, n.Registry, notifications.Intent{ID: "access-activation-code:" + challengeID, Destination: destination, Template: "Código para ativar acesso ao Inspection", Parameters: map[string]string{"body": text, "html": body}})
}

func deliver(ctx context.Context, registry *notifications.Registry, intent notifications.Intent) error {
	result := notifications.Deliver(ctx, registry, map[notifications.Channel]notifications.Intent{notifications.Email: intent})
	if result.Status == "DELIVERED" {
		return nil
	}
	if len(result.Attempts) > 0 && result.Attempts[0].Err != nil {
		return result.Attempts[0].Err
	}
	return errors.New("notification delivery failed")
}
