"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { graphql } from "@/graphql/client";
import { MarkNotificationReadDocument, MyNotificationsDocument, type MarkNotificationReadMutation, type MyNotificationsQuery } from "@/graphql/generated";

export type Notification = { id: string; kind: string; title: string; body: string; resourceKind: string; resourceId: string | null; createdAt: string; readAt: string | null };

export function useNotifications(enabled: boolean) {
  const [state, setState] = useState({ items: [] as Notification[], unreadCount: 0, cursor: null as string | null, error: "" }); const [visible, setVisible] = useState(true); const cursor = useRef<string | null>(null);
  const refresh = useCallback(async () => { if (!enabled || document.visibilityState === "hidden") return; try { cursor.current = null; const result = await graphql<MyNotificationsQuery, { after: string | null; unreadOnly: boolean }>(MyNotificationsDocument, { after: null, unreadOnly: false }); const next = result.myNotifications; cursor.current = next.pageInfo.endCursor; setState({ items: next.nodes, unreadCount: next.unreadCount, cursor: next.pageInfo.endCursor, error: "" }); } catch (error) { setState((current) => ({ ...current, error: (error as { message?: string }).message ?? "Não foi possível carregar notificações." })); } }, [enabled]);
  useEffect(() => { const visibility = () => setVisible(document.visibilityState !== "hidden"); visibility(); document.addEventListener("visibilitychange", visibility); return () => document.removeEventListener("visibilitychange", visibility); }, []);
  useEffect(() => { if (!enabled || !visible) return; void refresh(); const interval = window.setInterval(() => void refresh(), 30_000); const focus = () => void refresh(); window.addEventListener("focus", focus); return () => { window.clearInterval(interval); window.removeEventListener("focus", focus); }; }, [enabled, visible, refresh]);
  const markRead = useCallback(async (notificationId: string) => { const clientMutationId = crypto.randomUUID(); const result = await graphql<MarkNotificationReadMutation, { input: { notificationId: string; clientMutationId: string } }>(MarkNotificationReadDocument, { input: { notificationId, clientMutationId } }, clientMutationId); setState((current) => ({ ...current, unreadCount: result.markNotificationRead.notification?.readAt && current.items.some((item) => item.id === notificationId && !item.readAt) ? Math.max(0, current.unreadCount - 1) : current.unreadCount, items: current.items.map((item) => item.id === notificationId ? { ...item, readAt: result.markNotificationRead.notification?.readAt ?? item.readAt } : item) })); }, []);
  return { ...state, refresh, markRead, polling: enabled && visible };
}
