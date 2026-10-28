"use client";

// Counterparty CSRF proof is isolated from capture and deliberately memory-only.
let token: string | undefined;
export const setCounterpartyCsrfToken = (value: string): void => { token = value; };
export const getCounterpartyCsrfToken = (): string | undefined => token;
export const clearCounterpartyCsrfToken = (): void => { token = undefined; };
