export type OnboardingChoice = { value: string; label: string };

const legacyLabels: Record<string, string> = {
  Agency: "Imobiliária",
  Property: "Imóvel",
  "Reference photos": "Fotos de referência",
  Participant: "Responsável pela vistoria",
  "Agency name": "Nome da imobiliária",
  "Property address": "Endereço do imóvel",
  "Property type": "Tipo de imóvel",
  Rooms: "Quantidade de cômodos",
  Purpose: "Finalidade da vistoria",
  "Inspection deadline": "Prazo para concluir a vistoria",
  "Reference mode": "Base de comparação",
  "Who will inspect": "Quem realizará a vistoria?",
  "Participant name": "Nome do responsável",
  "Participant email": "E-mail do responsável",
  "Property overview": "Visão geral do imóvel",
};

const optionLabels: Record<string, string> = {
  APARTMENT: "Apartamento", HOUSE: "Casa", COMMERCIAL: "Imóvel comercial", LAND: "Terreno",
  SALE: "Venda", RENTAL: "Locação", MAINTENANCE: "Manutenção", INSURANCE: "Seguro",
  CHECKLIST_ONLY: "Primeira vistoria do imóvel", FIXED_ORIGIN: "Comparar com fotos de referência",
  SELF: "Eu farei a vistoria", DELEGATE: "Outra pessoa fará a vistoria",
};

const statusLabels: Record<string, string> = {
  PENDING: "Pendente", READY: "Pronta", NOT_REQUIRED: "Não se aplica", FAILED: "Falhou", NOT_STARTED: "Não iniciada",
  RETRY: "Tentar novamente", WAIT_FOR_DELIVERY: "Aguardar envio", REVIEW_AND_SUBMIT: "Revisar e enviar", CONTINUE_ONBOARDING: "Continuar cadastro",
};

export function presentOnboardingLabel(label: string) { return legacyLabels[label] ?? label; }
export function presentOnboardingOption(value: string, choices?: OnboardingChoice[]) {
  return choices?.find((choice) => choice.value === value)?.label ?? optionLabels[value] ?? "Opção não reconhecida";
}
export function presentOnboardingStatus(value: string) { return statusLabels[value] ?? "Situação não reconhecida"; }
