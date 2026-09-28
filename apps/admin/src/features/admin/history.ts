export type AdminHistoryTarget = Record<string, string | number | null | undefined>;

/** Keeps a resource detail panel scoped to the selected resource's history. */
export function isHistoryForResource(target: AdminHistoryTarget, eventTargetId: string): boolean {
  const targetId = target.targetId ?? target.inspectionId ?? target.ID;
  return targetId != null && String(targetId) === eventTargetId;
}
