"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { graphql } from "@/graphql/client";
import { MarkAllNotificationsReadDocument, MarkNotificationReadDocument, MyNotificationsDocument, NotificationDeliveriesDocument, type MarkAllNotificationsReadMutation, type MarkNotificationReadMutation, type MyNotificationsQuery, type NotificationDeliveriesQuery } from "@/graphql/generated";

export type Notification = { id: string; kind: string; title: string; body: string; resourceKind: string; resourceId: string | null; createdAt: string; readAt: string | null; action: string; context: Record<string, unknown>; priority: string; dueAt: string | null };
export type NotificationDelivery = NotificationDeliveriesQuery["notificationDeliveries"]["nodes"][number];
export type NotificationFilter = "unread" | "all";

const PAGE_SIZE = 25;

export function useNotifications(enabled: boolean, includeDeliveries: boolean) {
  const [filter, setFilter] = useState<NotificationFilter>("unread");
  const [kindFilter, setKindFilter] = useState("");
  const [projectFilter, setProjectFilter] = useState("");
  const [readingAll, setReadingAll] = useState(false);
  const [state, setState] = useState({
    items: [] as Notification[],
    unreadCount: 0,
    loadingNotifications: true,
    cursor: null as string | null,
    hasMoreNotifications: false,
    loadingMoreNotifications: false,
    deliveries: [] as NotificationDelivery[],
    deliveryCursor: null as string | null,
    hasMoreDeliveries: false,
    loadingDeliveries: false,
    error: "",
  });
  const [visible, setVisible] = useState(true);
  const loadedCount = useRef(PAGE_SIZE);
  const filterGeneration = useRef(0);

  const refresh = useCallback(async () => {
    if (!enabled || document.visibilityState === "hidden") return;
    const generation = filterGeneration.current;
    setState((current) => ({ ...current, loadingNotifications: current.items.length === 0 }));
    try {
      const variables = { after: null as string | null, unreadOnly: filter === "unread", kind: kindFilter || null, projectId: projectFilter || null };
      const firstPage = await graphql<MyNotificationsQuery, typeof variables>(MyNotificationsDocument, variables);
      const items = [...firstPage.myNotifications.nodes];
      let cursor = firstPage.myNotifications.pageInfo.endCursor;
      let hasMore = firstPage.myNotifications.pageInfo.hasNextPage;
      while (items.length < loadedCount.current && hasMore && cursor) {
        const page = await graphql<MyNotificationsQuery, typeof variables>(MyNotificationsDocument, { ...variables, after: cursor });
        items.push(...page.myNotifications.nodes.filter((item) => !items.some((existing) => existing.id === item.id)));
        cursor = page.myNotifications.pageInfo.endCursor;
        hasMore = page.myNotifications.pageInfo.hasNextPage;
      }
      if (generation !== filterGeneration.current) return;
      setState((current) => ({
        ...current,
        items,
        unreadCount: firstPage.myNotifications.unreadCount,
        loadingNotifications: false,
        cursor,
        hasMoreNotifications: hasMore,
        loadingMoreNotifications: false,
        error: "",
      }));
    } catch (error) {
      if (generation !== filterGeneration.current) return;
      setState((current) => ({ ...current, loadingNotifications: false, error: (error as { message?: string }).message ?? "Não foi possível carregar notificações." }));
    }
  }, [enabled, filter, kindFilter, projectFilter]);

  useEffect(() => {
    const visibility = () => setVisible(document.visibilityState !== "hidden");
    visibility();
    document.addEventListener("visibilitychange", visibility);
    return () => document.removeEventListener("visibilitychange", visibility);
  }, []);

  useEffect(() => {
    if (!enabled || !visible) return;
    void refresh();
    const interval = window.setInterval(() => void refresh(), 30_000);
    const focus = () => void refresh();
    window.addEventListener("focus", focus);
    return () => { window.clearInterval(interval); window.removeEventListener("focus", focus); };
  }, [enabled, visible, refresh]);

  const changeFilter = useCallback((nextFilter: NotificationFilter) => {
    if (nextFilter === filter) return;
    filterGeneration.current += 1;
    loadedCount.current = PAGE_SIZE;
    setFilter(nextFilter);
    setState((current) => ({ ...current, items: [], cursor: null, hasMoreNotifications: false, loadingNotifications: true, loadingMoreNotifications: false, error: "" }));
  }, [filter]);

  const changeKindFilter = useCallback((next: string) => {
    filterGeneration.current += 1;
    loadedCount.current = PAGE_SIZE;
    setKindFilter(next);
    setState((current) => ({ ...current, items: [], cursor: null, hasMoreNotifications: false, loadingNotifications: true, loadingMoreNotifications: false, error: "" }));
  }, []);

  const changeProjectFilter = useCallback((next: string) => {
    filterGeneration.current += 1;
    loadedCount.current = PAGE_SIZE;
    setProjectFilter(next);
    setState((current) => ({ ...current, items: [], cursor: null, hasMoreNotifications: false, loadingNotifications: true, loadingMoreNotifications: false, error: "" }));
  }, []);

  const loadMoreNotifications = useCallback(async () => {
    if (!enabled || !state.hasMoreNotifications || !state.cursor || state.loadingMoreNotifications) return;
    const generation = filterGeneration.current;
    setState((current) => ({ ...current, loadingMoreNotifications: true, error: "" }));
    try {
      const result = await graphql<MyNotificationsQuery, { after: string | null; unreadOnly: boolean; kind: string | null; projectId: string | null }>(MyNotificationsDocument, { after: state.cursor, unreadOnly: filter === "unread", kind: kindFilter || null, projectId: projectFilter || null });
      const next = result.myNotifications;
      if (generation !== filterGeneration.current) return;
      loadedCount.current += next.nodes.length;
      setState((current) => ({
        ...current,
        items: [...current.items, ...next.nodes.filter((item) => !current.items.some((existing) => existing.id === item.id))],
        unreadCount: next.unreadCount,
        cursor: next.pageInfo.endCursor,
        hasMoreNotifications: next.pageInfo.hasNextPage,
        loadingMoreNotifications: false,
      }));
    } catch (error) {
      if (generation !== filterGeneration.current) return;
      setState((current) => ({ ...current, loadingMoreNotifications: false, error: (error as { message?: string }).message ?? "Não foi possível carregar mais notificações." }));
    }
  }, [enabled, filter, kindFilter, projectFilter, state.cursor, state.hasMoreNotifications, state.loadingMoreNotifications]);

  const loadDeliveries = useCallback(async () => {
    if (!enabled || !includeDeliveries || state.loadingDeliveries) return;
    setState((current) => ({ ...current, loadingDeliveries: true, error: "" }));
    try {
      const result = await graphql<NotificationDeliveriesQuery, { after: string | null }>(NotificationDeliveriesDocument, { after: null });
      const next = result.notificationDeliveries;
      setState((current) => ({ ...current, deliveries: next.nodes, deliveryCursor: next.pageInfo.endCursor, hasMoreDeliveries: next.pageInfo.hasNextPage, loadingDeliveries: false }));
    } catch (error) {
      setState((current) => ({ ...current, loadingDeliveries: false, error: (error as { message?: string }).message ?? "Não foi possível carregar o estado das entregas." }));
    }
  }, [enabled, includeDeliveries, state.loadingDeliveries]);

  const loadMoreDeliveries = useCallback(async () => {
    if (!enabled || !includeDeliveries || !state.hasMoreDeliveries || !state.deliveryCursor) return;
    try {
      const result = await graphql<NotificationDeliveriesQuery, { after: string | null }>(NotificationDeliveriesDocument, { after: state.deliveryCursor });
      const next = result.notificationDeliveries;
      setState((current) => ({ ...current, deliveries: [...current.deliveries, ...next.nodes], deliveryCursor: next.pageInfo.endCursor, hasMoreDeliveries: next.pageInfo.hasNextPage }));
    } catch (error) {
      setState((current) => ({ ...current, error: (error as { message?: string }).message ?? "Não foi possível carregar mais entregas." }));
    }
  }, [enabled, includeDeliveries, state.deliveryCursor, state.hasMoreDeliveries]);

  const markRead = useCallback(async (notificationId: string) => {
    try {
      const clientMutationId = crypto.randomUUID();
      const result = await graphql<MarkNotificationReadMutation, { input: { notificationId: string; all: boolean; kind: string | null; projectId: string | null; through: string | null; clientMutationId: string } }>(MarkNotificationReadDocument, { input: { notificationId, all: false, kind: null, projectId: null, through: null, clientMutationId } }, clientMutationId);
      const readAt = result.markNotificationRead.notification?.readAt;
      setState((current) => {
        const wasUnread = current.items.some((item) => item.id === notificationId && !item.readAt);
        return {
          ...current,
          unreadCount: readAt && wasUnread ? Math.max(0, current.unreadCount - 1) : current.unreadCount,
          items: filter === "unread"
            ? current.items.filter((item) => item.id !== notificationId)
            : current.items.map((item) => item.id === notificationId ? { ...item, readAt: readAt ?? item.readAt } : item),
        };
      });
    } catch (error) {
      setState((current) => ({ ...current, error: (error as { message?: string }).message ?? "Não foi possível marcar a notificação como lida." }));
    }
  }, [filter]);

  const markAllRead = useCallback(async () => {
    if (!enabled || readingAll) return;
    setReadingAll(true);
    try {
      const clientMutationId = crypto.randomUUID();
      const result = await graphql<MarkAllNotificationsReadMutation, { input: { all: boolean; notificationId: string | null; kind: string | null; projectId: string | null; through: string; clientMutationId: string } }>(MarkAllNotificationsReadDocument, { input: { all: true, notificationId: null, kind: kindFilter || null, projectId: projectFilter || null, through: new Date().toISOString(), clientMutationId } }, clientMutationId);
      const payload = result.markNotificationRead;
      if (payload.userErrors.length) throw new Error(payload.userErrors.map((error) => error.message).join(" "));
      setState((current) => ({ ...current, unreadCount: payload.unreadCount, items: filter === "unread" ? [] : current.items.map((item) => ({ ...item, readAt: item.readAt ?? new Date().toISOString() })) }));
    } catch (error) {
      setState((current) => ({ ...current, error: (error as { message?: string }).message ?? "Não foi possível marcar todas como lidas." }));
    } finally {
      setReadingAll(false);
    }
  }, [enabled, filter, kindFilter, projectFilter, readingAll]);

  return {
    ...state,
    filter,
    changeFilter,
    kindFilter,
    changeKindFilter,
    projectFilter,
    changeProjectFilter,
    refresh,
    loadMoreNotifications,
    loadDeliveries,
    loadMoreDeliveries,
    markRead,
    markAllRead,
    readingAll,
    deliveryVisibility: includeDeliveries,
    polling: enabled && visible,
  };
}
