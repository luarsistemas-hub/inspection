"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { graphql } from "@/graphql/client";

export type Notification = { id: string; kind: string; title: string; body: string; resourceKind: string; resourceId: string | null; createdAt: string; readAt: string | null };
type Result = { myNotifications: { nodes: Notification[]; unreadCount: number; pageInfo: { endCursor: string | null; hasNextPage: boolean } } };
const query = "query MyNotifications($after:String){ myNotifications(first:25,after:$after){ nodes { id kind title body resourceKind resourceId createdAt readAt } unreadCount pageInfo { endCursor hasNextPage } } }";

export function useNotifications(enabled: boolean) {
  const [state, setState] = useState({ items: [] as Notification[], unreadCount: 0, cursor: null as string | null, error: "" }); const [visible, setVisible] = useState(true); const cursor = useRef<string | null>(null);
  const refresh = useCallback(async () => { if (!enabled || document.visibilityState === "hidden") return; try { cursor.current = null; const result = await graphql<Result>(query, { after: null }); const next = result.myNotifications; cursor.current = next.pageInfo.endCursor; setState({ items: next.nodes, unreadCount: next.unreadCount, cursor: next.pageInfo.endCursor, error: "" }); } catch (error) { setState((current) => ({ ...current, error: (error as { message?: string }).message ?? "Não foi possível carregar notificações." })); } }, [enabled]);
  useEffect(() => { const visibility = () => setVisible(document.visibilityState !== "hidden"); visibility(); document.addEventListener("visibilitychange", visibility); return () => document.removeEventListener("visibilitychange", visibility); }, []);
  useEffect(() => { if (!enabled || !visible) return; void refresh(); const interval = window.setInterval(() => void refresh(), 30_000); const focus = () => void refresh(); window.addEventListener("focus", focus); return () => { window.clearInterval(interval); window.removeEventListener("focus", focus); }; }, [enabled, visible, refresh]);
  const markRead = useCallback(async (notificationId: string) => { const clientMutationId = crypto.randomUUID(); const result = await graphql<{ markNotificationRead: { notification: Notification | null } }>("mutation MarkNotificationRead($input:MarkNotificationReadInput!){ markNotificationRead(input:$input){ notification { id readAt } } }", { input: { notificationId, clientMutationId } }, clientMutationId); setState((current) => ({ ...current, unreadCount: result.markNotificationRead.notification?.readAt && current.items.some((item) => item.id === notificationId && !item.readAt) ? Math.max(0, current.unreadCount - 1) : current.unreadCount, items: current.items.map((item) => item.id === notificationId ? { ...item, readAt: result.markNotificationRead.notification?.readAt ?? item.readAt } : item) })); }, []);
  return { ...state, refresh, markRead, polling: enabled && visible };
}
