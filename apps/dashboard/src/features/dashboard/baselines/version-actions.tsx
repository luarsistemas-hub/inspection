"use client";

import { useState } from "react";
import { Button, Field, Textarea } from "@inspection/design-system";
import { graphql } from "@/graphql/client";
import * as G from "@/graphql/generated";
import { deliveryPresentation } from "@/features/notifications/delivery-status";
import { counterpartyResponseLabel } from "../inventory-presentation";
import { failureText, newClientMutationId, throwUserErrors, type BaselineVersion } from "./review-model";

type Invitation = BaselineVersion["counterpartyInvitations"][number];

const deadlineFormat = new Intl.DateTimeFormat("pt-BR", { dateStyle: "medium" });

/** CounterpartyList shows each counterparty of a version with delivery, answer and pending actions. */
export function CounterpartyList({ invitations, participantNames, canReview, onChanged }: { invitations: Invitation[]; participantNames: Map<string, string>; canReview: boolean; onChanged: () => void }) {
  if (!invitations.length) return null;
  return <section className="baseline-counterparties" aria-label="Contrapartes da versão">
    <h4>Contrapartes</h4>
    <ul>
      {invitations.map((invitation) => {
        const name = participantNames.get(invitation.participantId) ?? "Contraparte";
        const delivery = deliveryPresentation(invitation.deliveryStatus ?? "UNKNOWN");
        return <li key={invitation.id}>
          <p><strong>{name}</strong> · {counterpartyResponseLabel(invitation.response)} · prazo até {deadlineFormat.format(new Date(invitation.deadlineAt))} · entrega: {delivery.label}</p>
          {canReview && invitation.response === "PENDING" && invitation.deliveryStatus === "FAILED" && <ResendInvitation invitationId={invitation.id} participantName={name} onDone={onChanged} />}
          {canReview && invitation.response === "CONTESTED" && invitation.resolution === "OPEN" && <ContestationResolution invitation={invitation} participantName={name} onDone={onChanged} />}
        </li>;
      })}
    </ul>
  </section>;
}

/** ContestationResolution declines a contestation with a reason or accepts it into a new draft. */
export function ContestationResolution({ invitation, participantName, onDone }: { invitation: Invitation; participantName: string; onDone: () => void }) {
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");
  const resolve = async (outcome: "ACCEPT" | "DECLINE") => {
    setBusy(true);
    setMessage("");
    try {
      const result = await graphql<G.ResolveBaselineContestationMutation, G.ResolveBaselineContestationMutationVariables>(G.ResolveBaselineContestationDocument, { input: { counterpartyInvitationId: invitation.id, outcome, reason: reason || null, clientMutationId: newClientMutationId() } });
      throwUserErrors(result.resolveBaselineContestation.userErrors);
      setMessage(outcome === "ACCEPT" ? "Contestação aceita; um novo rascunho foi criado." : "Contestação recusada com justificativa.");
      onDone();
    } catch (error) {
      setMessage(failureText(error));
    } finally {
      setBusy(false);
    }
  };
  return <section className="baseline-contestation" aria-label={`Contestação de ${participantName}`}>
    <h5>Contestação recebida</h5>
    <p>{invitation.statement}</p>
    <Field label="Justificativa para recusar"><Textarea value={reason} onChange={(event) => setReason(event.target.value)} maxLength={4000} /></Field>
    <Button disabled={busy || !reason.trim()} onClick={() => void resolve("DECLINE")}>Recusar contestação</Button>
    <Button variant="secondary" disabled={busy} onClick={() => void resolve("ACCEPT")}>Aceitar e criar rascunho</Button>
    {message && <p role="status">{message}</p>}
  </section>;
}

/** ResendInvitation resends a counterparty invitation whose delivery failed. */
export function ResendInvitation({ invitationId, participantName, onDone }: { invitationId: string; participantName: string; onDone: () => void }) {
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");
  const resend = async () => {
    setBusy(true);
    try {
      const result = await graphql<G.ResendCounterpartyInvitationMutation, G.ResendCounterpartyInvitationMutationVariables>(G.ResendCounterpartyInvitationDocument, { input: { counterpartyInvitationId: invitationId, clientMutationId: newClientMutationId() } });
      throwUserErrors(result.resendCounterpartyInvitation.userErrors);
      setMessage("Convite reenviado.");
      onDone();
    } catch (error) {
      setMessage(failureText(error));
    } finally {
      setBusy(false);
    }
  };
  return <p>Falha na entrega do convite. <Button variant="secondary" aria-label={`Reenviar convite para ${participantName}`} disabled={busy} onClick={() => void resend()}>Reenviar</Button>{message && <span role="status">{message}</span>}</p>;
}
