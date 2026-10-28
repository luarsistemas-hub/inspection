export function isValidBaselinePolicyDays(value: number): boolean {
  return Number.isInteger(value) && value >= 1 && value <= 90;
}

export function isValidSupersededRetentionDays(value: number): boolean {
  return Number.isInteger(value) && value >= 1 && value <= 3650;
}
