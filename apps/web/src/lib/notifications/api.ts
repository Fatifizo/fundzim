import { browserApi, type ApiClient } from "@/lib/api/client";

/** Browser-side notifications calls through the same-origin /api/v1 proxy. Step-up is handled by the caller (useStepUp). */
const enc = encodeURIComponent;
const A = "/api/v1/admin/communications";
const T = 20_000;

export function createNotificationsApi(api: ApiClient = browserApi) {
  const post = <T>(path: string, body: unknown = {}, key?: string) =>
    api.post<T>(path, { body, timeoutMs: T, headers: key ? { "Idempotency-Key": key } : undefined }).then((r) => r.data);
  return {
    unread: () => api.get<{ unread: number }>("/api/v1/notifications/unread-count", { timeoutMs: 8_000 }).then((r) => r.data),
    markRead: (id: string) => post<unknown>(`/api/v1/notifications/${enc(id)}/read`),
    markAllRead: () => post<{ marked_read: number }>("/api/v1/notifications/read-all"),
    setPreferences: (changes: Array<{ category: string; channel: string; enabled: boolean }>) =>
      api.put<unknown>("/api/v1/notifications/preferences", { body: { changes }, timeoutMs: T }).then((r) => r.data),
    setConsent: (channel: string, purpose: string, granted: boolean) => post<unknown>("/api/v1/communications/consent", { channel, purpose, granted }),
    replay: (id: string, reason: string) => post<{ replayed: boolean; blocked_reason?: string; delivery_status: string }>(`${A}/dead-letters/${enc(id)}/replay`, { reason }),
    discard: (id: string, reason: string) => post<unknown>(`${A}/dead-letters/${enc(id)}/discard`, { reason }),
    suppress: (body: { user_id: string; channel: string; reason: string }, key: string) => post<unknown>(`${A}/suppressions`, body, key),
    lift: (id: string, reason: string) => post<unknown>(`${A}/suppressions/${enc(id)}/lift`, { reason }),
    createDraft: (templateId: string, body: Record<string, string | null>, key: string) => post<{ id: string }>(`${A}/templates/${enc(templateId)}/versions`, body, key),
    templateAction: (versionId: string, action: string, body: Record<string, unknown>) => post<unknown>(`${A}/template-versions/${enc(versionId)}/${action}`, body),
    preview: (versionId: string) => post<Record<string, Record<string, unknown>>>(`${A}/template-versions/${enc(versionId)}/preview`),
  };
}

export type NotificationsApi = ReturnType<typeof createNotificationsApi>;
export const notificationsApi = createNotificationsApi();
