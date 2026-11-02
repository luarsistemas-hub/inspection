/** matchesEmailConfirmation compares addresses without case or outer whitespace differences. */
export function matchesEmailConfirmation(email: string, confirmation: string): boolean {
  return email.trim().toLowerCase() === confirmation.trim().toLowerCase();
}
