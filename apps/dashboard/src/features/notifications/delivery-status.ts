export const notificationStates = ["QUEUED", "PROCESSING", "ACCEPTED", "SENT", "DELIVERED", "FAILED", "UNKNOWN", "CANCELED"] as const;

export type NotificationState = (typeof notificationStates)[number];

type DeliveryChannel = { status: string; attempts: number };

export type DeliveryPresentation = {
  label: string;
  description: string;
  needsReview: boolean;
  retryExhausted: boolean;
  partial: boolean;
};

const presentations: Record<NotificationState, Omit<DeliveryPresentation, "partial" | "retryExhausted">> = {
  QUEUED: { label: "Solicitada", description: "A solicitação foi registrada e aguarda processamento.", needsReview: false },
  PROCESSING: { label: "Em processamento", description: "O canal está sendo processado.", needsReview: false },
  ACCEPTED: { label: "Aceita pelo provedor", description: "O provedor aceitou a solicitação; isso não confirma entrega.", needsReview: false },
  SENT: { label: "Enviada", description: "O envio foi iniciado, mas a entrega ainda não foi confirmada.", needsReview: false },
  DELIVERED: { label: "Entregue", description: "A entrega foi confirmada pelo provedor.", needsReview: false },
  FAILED: { label: "Falhou", description: "Não foi possível concluir a entrega neste canal.", needsReview: false },
  UNKNOWN: { label: "Revisão necessária", description: "O resultado é incerto. Revise antes de tentar outra ação.", needsReview: true },
  CANCELED: { label: "Cancelada", description: "A entrega foi cancelada antes da conclusão.", needsReview: false },
};

function normalizeState(value: string): NotificationState {
  return notificationStates.includes(value as NotificationState) ? value as NotificationState : "UNKNOWN";
}

export function isPartialDelivery(channels: DeliveryChannel[]): boolean {
  return channels.some((channel) => normalizeState(channel.status) === "DELIVERED") && channels.some((channel) => normalizeState(channel.status) !== "DELIVERED");
}

export function deliveryPresentation(status: string, channels: DeliveryChannel[] = []): DeliveryPresentation {
  const normalized = normalizeState(status);
  const retryExhausted = normalized === "FAILED" && channels.some((channel) => normalizeState(channel.status) === "FAILED" && channel.attempts >= 4);
  return { ...presentations[normalized], partial: isPartialDelivery(channels), retryExhausted };
}
