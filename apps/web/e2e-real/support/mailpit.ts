import { MAILPIT_URL } from "./env";

/**
 * Mailpit (the local mail catcher) receives every email the worker sends and, with SMS_PROVIDER=dev_mailpit,
 * every SMS as an email to <e164 digits>@sms.dev.invalid. Only its HTTP API is used.
 */
interface MailpitSummary {
  ID: string;
  Subject: string;
  Created: string;
}

async function search(to: string): Promise<MailpitSummary[]> {
  const res = await fetch(`${MAILPIT_URL}/api/v1/search?query=${encodeURIComponent(`to:"${to}"`)}&limit=50`);
  if (!res.ok) throw new Error(`Mailpit search failed: HTTP ${res.status}`);
  const body = (await res.json()) as { messages?: MailpitSummary[] };
  return body.messages ?? [];
}

async function text(id: string): Promise<string> {
  const res = await fetch(`${MAILPIT_URL}/api/v1/message/${encodeURIComponent(id)}`);
  if (!res.ok) throw new Error(`Mailpit message failed: HTTP ${res.status}`);
  return ((await res.json()) as { Text?: string }).Text ?? "";
}

/**
 * Waits for the newest message to `to` whose subject contains `subject` (and, when given, that is not one
 * of `seen` message ids) and returns its id and text body.
 */
export async function waitForMail(to: string, subject: string, opts: { timeoutMs?: number; exclude?: Set<string> } = {}) {
  const deadline = Date.now() + (opts.timeoutMs ?? 60_000);
  while (Date.now() < deadline) {
    for (const m of await search(to)) {
      if (m.Subject.includes(subject) && !opts.exclude?.has(m.ID)) return { id: m.ID, text: await text(m.ID) };
    }
    await new Promise((r) => setTimeout(r, 400));
  }
  throw new Error(`No Mailpit message to ${to} with subject containing "${subject}" within the timeout (is the worker running?)`);
}

/** Ids of every message already delivered to `to` (to wait for a newer one later). */
export async function messageIds(to: string): Promise<Set<string>> {
  return new Set((await search(to)).map((m) => m.ID));
}

const TOKEN_RE = /token=([A-Za-z0-9_-]{16,256})/;

/** The single-use token from a link in an email body. */
export function tokenFrom(body: string): string {
  const m = TOKEN_RE.exec(body);
  if (!m) throw new Error(`No token link in email body: ${body.slice(0, 300)}`);
  return m[1]!;
}

/** The full https?:// link containing the token, as written in the email. */
export function linkFrom(body: string): string {
  const m = /(https?:\/\/\S*token=[A-Za-z0-9_-]{16,256})/.exec(body);
  if (!m) throw new Error(`No link in email body: ${body.slice(0, 300)}`);
  return m[1]!;
}
