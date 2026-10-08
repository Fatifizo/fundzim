"use client";

import { useRouter } from "next/navigation";
import { useState } from "react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { authApi, type AuthApi } from "@/lib/auth/api";
import { describeError, isAuthRequired } from "@/lib/auth/errors";
import { loginUrl } from "@/lib/auth/safe-redirect";
import type { SessionItem } from "@/lib/auth/types";
import { browserNavigation } from "@/lib/browser-navigation";

import { formatDateTime } from "./format";

export function SessionsList({ sessions, api = authApi }: { sessions: SessionItem[]; api?: AuthApi }) {
  const router = useRouter();
  const [busyId, setBusyId] = useState<string | null>(null);
  const [message, setMessage] = useState<{ tone: "ok" | "error"; text: string } | null>(null);

  function handle(error: unknown, fallback: string) {
    if (isAuthRequired(error)) {
      browserNavigation.assign(loginUrl("/settings/sessions"));
      return;
    }
    setMessage({ tone: "error", text: describeError(error, fallback).message });
  }

  async function revoke(session: SessionItem) {
    setBusyId(session.id);
    setMessage(null);
    try {
      await api.revokeSession(session.id);
      setMessage({ tone: "ok", text: "That session has been signed out." });
      router.refresh();
    } catch (error) {
      handle(error, "We couldn't sign out that session. Please try again.");
    } finally {
      setBusyId(null);
    }
  }

  async function logoutAll() {
    setBusyId("all");
    setMessage(null);
    try {
      await api.logoutAll();
      browserNavigation.assign("/login");
      return;
    } catch (error) {
      handle(error, "We couldn't sign out your sessions. Please try again.");
    }
    setBusyId(null);
  }

  return (
    <div className="space-y-6">
      {message ? (
        <p role={message.tone === "error" ? "alert" : "status"} className={message.tone === "error" ? "font-semibold text-danger-700" : "font-semibold text-brand-800"}>
          {message.text}
        </p>
      ) : null}
      {sessions.length === 0 ? <p className="text-ink-700">No active sessions.</p> : null}
      <ul className="space-y-3">
        {sessions.map((session) => (
          <li key={session.id} className="rounded-card border border-line bg-surface p-4 shadow-card">
            <div className="flex flex-wrap items-start justify-between gap-3">
              <div className="min-w-0 space-y-1">
                <p className="font-semibold break-words text-ink-900">
                  {session.user_agent || "Unknown device"}{" "}
                  {session.current ? <Badge tone="brand">This device</Badge> : null}
                </p>
                <p className="text-sm text-ink-600">
                  Signed in {formatDateTime(session.created_at)} · last active {formatDateTime(session.last_seen_at)}
                </p>
                <p className="text-sm text-ink-600">
                  Approximate address {session.ip_masked || "unknown"} · {session.mfa ? "with two-step verification" : "password only"}
                </p>
              </div>
              {session.current ? null : (
                <Button variant="outline" onClick={() => revoke(session)} disabled={busyId !== null} aria-label={`Sign out session on ${session.user_agent || "unknown device"}`}>
                  {busyId === session.id ? "Signing out…" : "Sign out"}
                </Button>
              )}
            </div>
          </li>
        ))}
      </ul>
      <section aria-labelledby="logout-all-title" className="space-y-2">
        <h2 id="logout-all-title" className="font-display text-xl font-semibold text-ink-900">
          Sign out everywhere
        </h2>
        <p className="text-ink-700">Signs out every session, including this one. Use this if you think someone else has access to your account.</p>
        <Button variant="primary" onClick={logoutAll} disabled={busyId !== null}>
          {busyId === "all" ? "Signing out…" : "Sign out of all sessions"}
        </Button>
      </section>
    </div>
  );
}
