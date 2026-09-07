"use client";
import { useEffect } from "react";
export function RegisterServiceWorker() {
  useEffect(() => { if ("serviceWorker" in navigator) void navigator.serviceWorker.register("/sw.js"); }, []);
  return null;
}
