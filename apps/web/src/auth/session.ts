"use client";

// Deliberately module-scoped: dashboard credentials disappear with a reload.
let accessToken: string | undefined;

export function setAccessToken(token: string): void { accessToken = token; }
export function getAccessToken(): string | undefined { return accessToken; }
export function clearAccessToken(): void { accessToken = undefined; }
