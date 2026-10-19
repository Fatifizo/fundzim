"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useEffect, useRef, useState } from "react";

import { FormError, FormStatus } from "@/components/forms/form-error";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { formatDateTime } from "@/components/account/format";
import { humanise } from "@/lib/payments/labels";
import { notificationsApi, type NotificationsApi } from "@/lib/notifications/api";
import type { InboxItem } from "@/lib/notifications/types";

/**
 * The caller's in-app notifications. Marking read is the only action; links stay inside FundZim (the API only stores
 * relative links, and every linked page checks access itself).
 */
export function Inbox({ items, api = notificationsApi }: { items: InboxItem[]; api?: NotificationsApi }) {
  const router = useRouter();
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const noticeRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (notice) noticeRef.current?.focus();
  }, [notice]);
  const run = async (key: string, fn: () => Promise<unknown>, done: string) => {
    setBusy(key);
    setError(null);
    setNotice(null);
    try {
      await fn();
      setNotice(done);
      router.refresh();
    } catch {
      setError("That did not work. Reload the page and try again.");
    } finally {
      setBusy(null);
    }
  };
  const unread = items.filter((i) => !i.read_at).length;
  return (
    <div className="space-y-4">
      {notice ? <FormStatus ref={noticeRef} title={notice} /> : null}
      <FormError message={error} />
      {unread > 0 ? (
        <Button variant="outline" disabled={busy !== null} onClick={() => run("all", () => api.markAllRead(), "All notifications marked as read.")}>
          Mark all as read
        </Button>
      ) : null}
      {items.length === 0 ? (
        <p className="rounded-card border border-line bg-surface p-4 text-ink-700">No notifications to show.</p>
      ) : (
        <ul className="space-y-3" aria-label="Notifications">
          {items.map((n) => (
            <li key={n.id} className={`space-y-2 rounded-card border p-4 ${n.read_at ? "border-line bg-surface" : "border-brand-100 bg-brand-50"}`}>
              <div className="flex flex-wrap items-center gap-2">
                <h2 className="font-semibold text-ink-900">
                  {n.read_at ? null : <span className="sr-only">Unread: </span>}
                  {n.title}
                </h2>
                {n.test_data ? <Badge tone="gold">TEST data</Badge> : null}
                {n.priority === "HIGH" ? <Badge tone="info">Important</Badge> : null}
                {n.read_at ? null : <Badge tone="brand">Unread</Badge>}
              </div>
              <p className="text-ink-700">{n.body}</p>
              <p className="text-sm text-ink-600">
                {humanise(n.category)} · {formatDateTime(n.created_at)}
              </p>
              <div className="flex flex-wrap gap-3">
                {n.deep_link ? (
                  <Link href={n.deep_link} className="inline-flex min-h-11 items-center font-semibold underline underline-offset-4">
                    Open
                  </Link>
                ) : null}
                {n.read_at ? null : (
                  <Button variant="outline" aria-label={`Mark as read: ${n.title}`} disabled={busy !== null} onClick={() => run(n.id, () => api.markRead(n.id), "Marked as read.")}>
                    Mark as read
                  </Button>
                )}
              </div>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

/** Unread count link (polls every 60 s: no real-time infrastructure is needed for in-app notices). */
export function UnreadLink({ href, api = notificationsApi }: { href: string; api?: NotificationsApi }) {
  const [state, setState] = useState<number | "loading" | "error">("loading");
  useEffect(() => {
    let live = true;
    const load = () =>
      api
        .unread()
        .then((r) => live && setState(r.unread))
        .catch(() => live && setState("error"));
    void load();
    const id = setInterval(load, 60_000);
    return () => {
      live = false;
      clearInterval(id);
    };
  }, [api]);
  const label = state === "loading" ? "Notifications" : state === "error" ? "Notifications (could not load)" : `Notifications: ${state} unread`;
  return (
    <Link href={href} prefetch={false} className="inline-flex min-h-11 items-center rounded-full border border-line px-4 font-semibold text-ink-900 underline-offset-4 hover:underline">
      <span aria-live="polite">{label}</span>
    </Link>
  );
}
