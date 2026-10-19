"use client";

import { useRouter } from "next/navigation";
import { useEffect, useRef, useState, type FormEvent } from "react";

import { StepUpCancelledError } from "@/components/auth/step-up";
import { FormError, FormStatus } from "@/components/forms/form-error";
import { SubmitButton } from "@/components/forms/submit-button";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { humanise } from "@/lib/payments/labels";
import { notificationsApi, type NotificationsApi } from "@/lib/notifications/api";
import type { Consent, Preference } from "@/lib/notifications/types";

const CHANNEL_LABEL: Record<string, string> = { IN_APP: "In the app", EMAIL: "E-mail", SMS: "SMS", WHATSAPP: "WhatsApp" };
const ORDER = ["IN_APP", "EMAIL", "SMS", "WHATSAPP"];

/** Notification preferences: one checkbox per category and channel; mandatory and unavailable channels are explained. */
export function PreferencesForm({ items, api = notificationsApi }: { items: Preference[]; api?: NotificationsApi }) {
  const router = useRouter();
  const [state, setState] = useState(() => Object.fromEntries(items.map((p) => [`${p.category}|${p.channel}`, p.enabled])));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const noticeRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (notice) noticeRef.current?.focus();
  }, [notice]);
  const categories = [...new Set(items.map((p) => p.category))];
  async function submit(e: FormEvent) {
    e.preventDefault();
    const changes = items.filter((p) => !p.mandatory && p.available && state[`${p.category}|${p.channel}`] !== p.enabled)
      .map((p) => ({ category: p.category, channel: p.channel, enabled: state[`${p.category}|${p.channel}`]! }));
    if (changes.length === 0) {
      setNotice("Nothing to save.");
      return;
    }
    setBusy(true);
    setError(null);
    try {
      await api.setPreferences(changes);
      setNotice("Preferences saved.");
      router.refresh();
    } catch (err) {
      if (!(err instanceof StepUpCancelledError)) setError("Your preferences could not be saved. Reload the page and try again.");
    } finally {
      setBusy(false);
    }
  }
  return (
    <form onSubmit={submit} aria-label="Notification preferences" className="space-y-4">
      {notice ? <FormStatus ref={noticeRef} title={notice} /> : null}
      <FormError message={error} />
      {categories.map((cat) => (
        <fieldset key={cat} className="space-y-2 rounded-card border border-line bg-surface p-4">
          <legend className="px-1 font-semibold text-ink-900">{humanise(cat)}</legend>
          <ul className="grid gap-2 sm:grid-cols-2">
            {items
              .filter((p) => p.category === cat)
              .sort((a, b) => ORDER.indexOf(a.channel) - ORDER.indexOf(b.channel))
              .map((p) => {
                const id = `pref-${cat}-${p.channel}`;
                const locked = p.mandatory || !p.available;
                const hint = p.mandatory ? "Always on" : !p.available ? "Not available" : p.consent_required ? "Also needs your consent below" : null;
                return (
                  <li key={p.channel} className="flex items-start gap-3">
                    <input id={id} type="checkbox" className="mt-1 size-6 shrink-0 accent-brand-700" checked={locked ? p.enabled && p.available : state[`${cat}|${p.channel}`]}
                      disabled={locked} aria-describedby={hint ? `${id}-hint` : undefined}
                      onChange={(e) => setState((s) => ({ ...s, [`${cat}|${p.channel}`]: e.target.checked }))} />
                    <label htmlFor={id} className="text-ink-900">
                      {CHANNEL_LABEL[p.channel] ?? p.channel}
                      {hint ? (
                        <span id={`${id}-hint`} className="block text-sm text-ink-600">
                          {hint}
                        </span>
                      ) : null}
                    </label>
                  </li>
                );
              })}
          </ul>
        </fieldset>
      ))}
      <p className="text-sm text-ink-600">Quiet hours are not available. Security notices by e-mail and in the app cannot be turned off.</p>
      <SubmitButton busy={busy}>Save preferences</SubmitButton>
    </form>
  );
}

const STATUS_LABEL: Record<string, string> = { NOT_REQUESTED: "Not given", GRANTED: "Given", WITHDRAWN: "Withdrawn", EXPIRED: "Expired",
  REQUIRES_RECONFIRMATION: "Needs to be confirmed again" };

/**
 * Consent to receive service messages by SMS or WhatsApp (given or withdrawn only by you, recorded with the consent
 * wording version). Marketing consent cannot be given: FundZim does not send marketing messages.
 */
export function ConsentPanel({ items, api = notificationsApi }: { items: Consent[]; api?: NotificationsApi }) {
  const router = useRouter();
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const noticeRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (notice) noticeRef.current?.focus();
  }, [notice]);
  const change = async (c: Consent, granted: boolean) => {
    const key = `${c.channel}|${c.purpose}`;
    setBusy(key);
    setError(null);
    setNotice(null);
    try {
      await api.setConsent(c.channel, c.purpose, granted);
      setNotice(granted ? `Consent for ${CHANNEL_LABEL[c.channel]} recorded.` : `Consent for ${CHANNEL_LABEL[c.channel]} withdrawn.`);
      router.refresh();
    } catch {
      setError("Your choice could not be recorded. Reload the page and try again.");
    } finally {
      setBusy(null);
    }
  };
  const service = items.filter((c) => c.purpose === "SERVICE_MESSAGES" && c.channel !== "EMAIL");
  const marketing = items.filter((c) => c.purpose === "MARKETING");
  return (
    <div className="space-y-4">
      {notice ? <FormStatus ref={noticeRef} title={notice} /> : null}
      <FormError message={error} />
      <ul className="space-y-3">
        {service.map((c) => (
          <li key={c.channel} className="flex flex-col gap-2 rounded-card border border-line bg-surface p-4 sm:flex-row sm:items-center sm:justify-between">
            <div>
              <p className="font-semibold text-ink-900">Service messages by {CHANNEL_LABEL[c.channel]}</p>
              <p className="text-sm text-ink-600">
                Status: <Badge tone={c.status === "GRANTED" ? "brand" : "neutral"}>{STATUS_LABEL[c.status] ?? humanise(c.status)}</Badge>
              </p>
            </div>
            {c.status === "GRANTED" ? (
              <Button variant="outline" aria-label={`Withdraw consent for ${CHANNEL_LABEL[c.channel]}`} disabled={busy !== null} onClick={() => change(c, false)}>
                Withdraw consent
              </Button>
            ) : (
              <Button variant="outline" aria-label={`${c.status === "REQUIRES_RECONFIRMATION" ? "Confirm consent again" : "Give consent"} for ${CHANNEL_LABEL[c.channel]}`}
                disabled={busy !== null} onClick={() => change(c, true)}>
                {c.status === "REQUIRES_RECONFIRMATION" ? "Confirm again" : "Give consent"}
              </Button>
            )}
          </li>
        ))}
      </ul>
      <p className="rounded-card border border-line bg-surface-muted p-4 text-ink-700">
        Marketing messages: not available. FundZim does not send marketing messages, so consent to them cannot be given ({marketing.length} channels).
      </p>
      <p className="text-sm text-ink-600">The consent wording is pending legal review; no message is sent by SMS or WhatsApp in this test environment.</p>
    </div>
  );
}
