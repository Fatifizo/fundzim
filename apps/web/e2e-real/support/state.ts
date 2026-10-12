import fs from "node:fs";
import path from "node:path";

import { STATE_DIR } from "./env";
import type { StoredCookie } from "./http";

/**
 * Working state in apps/web/e2e-real/.state (git-ignored, mode 0600).
 *
 * - fixture.json: the persistent STAFF fixture (bootstrap admins and role holders, with their TOTP secrets
 *   and live session cookies). Creating staff costs a bootstrap ceremony, invitations and maker-checker
 *   grants, and the API's per-IP limits (auth.token.ip 20/15 min, auth.login.ip 20/5 min) are shared by
 *   everything this suite does, so staff are created once per database and reused while they still work.
 *   Every entry is re-validated against the API before use and recreated when it no longer works (for
 *   example after `docker compose down -v`, or after the Go integration tests revoked the super admins).
 * - run.json: what this run's global setup provisioned (owner, campaigns, the per-run linked staff member).
 *
 * Synthetic test accounts on a local or CI stack only.
 */
export interface StaffCred {
  key: string;
  name: string;
  role: string | null;
  email: string;
  id: string;
  secret: string;
}

export interface Fixture {
  version: 1;
  staff: Record<string, StaffCred>;
  sessions: Record<string, { label: string; cookies: StoredCookie[] }>;
  /** Unlinked REVIEWERs reserved for the linked-account test; each run takes one. */
  linkCandidates?: StaffCred[];
}

export interface CampaignRef {
  id: string;
  title: string;
  slug: string | null;
  category: string;
  /** Text that must never reach a public page. */
  privateMarkers?: string[];
}

export interface RunData {
  runId: string;
  createdAt: string;
  owner: {
    email: string;
    id: string;
    displayName: string;
    legalFirstName: string;
    legalLastName: string;
    beneficiaryId: string;
    session: { label: string; cookies: StoredCookie[] };
  };
  linkedStaff: StaffCred;
  staffEmails: { reviewer: string; compliance: string };
  catalogue: { token: string; campaigns: CampaignRef[]; byCategory: Record<string, number> };
  campaigns: {
    modStandard: CampaignRef;
    modMedical: CampaignRef;
    modMedical2: CampaignRef;
    linkAssign: CampaignRef;
    linkApprove: CampaignRef;
    pubDetail: CampaignRef;
    pubMedical: CampaignRef;
    pubSuspended: CampaignRef;
    pubUnlisted: CampaignRef;
    pubDraft: CampaignRef;
  };
  updates: { visibleTitle: string; hiddenTitle: string; pendingTitle: string };
}

const FIXTURE = path.join(STATE_DIR, "fixture.json");
const RUN = path.join(STATE_DIR, "run.json");

function writeJson(file: string, value: unknown) {
  fs.mkdirSync(STATE_DIR, { recursive: true, mode: 0o700 });
  const tmp = `${file}.${process.pid}.tmp`;
  fs.writeFileSync(tmp, JSON.stringify(value, null, 2), { mode: 0o600 });
  fs.renameSync(tmp, file);
}

export function loadFixture(): Fixture {
  try {
    const f = JSON.parse(fs.readFileSync(FIXTURE, "utf8")) as Fixture;
    if (f.version === 1) return f;
  } catch {
    // first run
  }
  return { version: 1, staff: {}, sessions: {} };
}

export function saveFixture(f: Fixture) {
  writeJson(FIXTURE, f);
}

export function saveRun(r: RunData) {
  writeJson(RUN, r);
}

export function loadRun(): RunData {
  try {
    return JSON.parse(fs.readFileSync(RUN, "utf8")) as RunData;
  } catch (error) {
    throw new Error(`No run data in ${RUN}: global setup did not complete (${String(error)})`);
  }
}

/** Per-test browser storage states (staff sessions signed in through the UI). */
export function storageStatePath(name: string) {
  fs.mkdirSync(STATE_DIR, { recursive: true, mode: 0o700 });
  return path.join(STATE_DIR, `storage-${name}.json`);
}

/** Loads the TOTP secret of a staff member by email from the fixture or the run. */
export function staffByEmail(email: string): StaffCred {
  const run = loadRun();
  if (run.linkedStaff.email === email) return run.linkedStaff;
  const f = loadFixture();
  const s = Object.values(f.staff).find((x) => x.email === email);
  if (!s) throw new Error(`unknown staff member ${email}`);
  return s;
}
