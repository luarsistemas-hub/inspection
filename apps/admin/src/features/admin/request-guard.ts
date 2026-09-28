/** Fences overlapping Admin collection requests when their membership scope changes. */
export class RequestGuard {
  private revision = 0;

  begin(): number {
    this.revision += 1;
    return this.revision;
  }

  invalidate(): void {
    this.revision += 1;
  }

  isCurrent(revision: number, signal?: AbortSignal): boolean {
    return revision === this.revision && !signal?.aborted;
  }
}
