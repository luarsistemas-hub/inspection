// datetime-local values have no offset; Date interprets them in the browser's local zone.
export function localDateTimeToInstant(value: string): string {
  const instant = new Date(value);
  if (Number.isNaN(instant.getTime())) throw new RangeError("invalid local date and time");
  return instant.toISOString();
}
