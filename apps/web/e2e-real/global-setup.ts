import { MAILPIT_URL, WEB_URL } from "./support/env";
import {
  approveAndPublish,
  approveKyc,
  attestAge,
  campaignStatus,
  createReadyCampaign,
  decision,
  ensureStaff,
  mapLimit,
  registerVerifiedUser,
  staffPool,
  submitCampaign,
  submitKyc,
  tag,
  takeLinkCandidate,
  type CampaignSpec,
} from "./support/provision";
import { loadFixture, saveFixture, saveRun, type CampaignRef, type RunData } from "./support/state";

/**
 * Provisions everything the real-stack specs need, through the real API and Mailpit:
 *  - the persistent staff fixture (bootstrap ceremony, invitations, maker-checker role grants), reused across
 *    runs while it still works;
 *  - per run: one verified, identity-verified owner (KYC documents scanned by ClamAV, approved by a KYC
 *    reviewer), one new REVIEWER staff member for the linked-account test, and the campaigns below.
 * The moderation steps under test are NOT done here: the specs perform them in the browser.
 */
function log(m: string) {
  console.log(`[real-stack setup] ${m}`);
}

async function preflight() {
  const checks: [string, string][] = [
    ["web", `${WEB_URL}/healthz`],
    ["api (through the web proxy)", `${WEB_URL}/api/v1/ready`],
    ["mailpit", `${MAILPIT_URL}/api/v1/info`],
  ];
  for (const [name, url] of checks) {
    let ok = false;
    try {
      ok = (await fetch(url, { signal: AbortSignal.timeout(10_000) })).ok;
    } catch {
      ok = false;
    }
    if (!ok) throw new Error(`${name} is not reachable at ${url}. Start the full stack first: docker compose up -d --build (see e2e-real/README.md).`);
  }
}

export default async function globalSetup() {
  const t0 = Date.now();
  await preflight();
  const fixture = loadFixture();
  const pool = staffPool(fixture);
  await ensureStaff(pool, log);
  const S = fixture.staff;
  const [kycReviewer, provReviewer, provCompliance, moderator] = await Promise.all([
    pool.session(S.kycReviewer!),
    pool.session(S.provReviewer!),
    pool.session(S.provCompliance!),
    pool.session(S.moderator!),
  ]);
  log(`staff ready (${Math.round((Date.now() - t0) / 1000)} s)`);

  const runId = tag(6);
  const legal = { first: "Chiedza", last: `Marufu${runId.replace(/[^a-z]/g, "").slice(0, 3)}` };
  const owner = await registerVerifiedUser(`owner-${runId}`, `Tendai Organiser ${runId.toUpperCase()}`);
  await attestAge(owner);
  const kycCase = await submitKyc(owner, legal);
  await approveKyc(kycReviewer, kycCase, owner);
  const ben = await owner.session.post("/beneficiaries", { beneficiary_type: "SELF" }, 201);
  const beneficiaryId = (ben.data as { id: string }).id;
  log(`owner ${owner.email} identity-verified (${Math.round((Date.now() - t0) / 1000)} s)`);

  // A staff member for the linked-account test: a staff account can be linked only once and a link is
  // permanent, so each run takes a fresh, unlinked REVIEWER from the fixture's reserve.
  const linkedStaff = takeLinkCandidate(fixture);

  const T = runId.toUpperCase();
  const token = `zq${runId}`;
  const specs: Record<string, CampaignSpec & { cover?: boolean }> = {
    modStandard: { title: `Help Tariro go to school ${T}`, category: "EDUCATION" },
    modMedical: { title: `Surgery for Rudo in Harare ${T}`, category: "MEDICAL", summary: "Help Rudo pay for an operation at Parirenyatwa hospital." },
    modMedical2: { title: `Physiotherapy for Nyasha ${T}`, category: "MEDICAL", summary: "Help Nyasha pay for physiotherapy after an accident." },
    linkAssign: { title: `Linked account campaign one ${T}`, category: "EDUCATION" },
    linkApprove: { title: `Linked account campaign two ${T}`, category: "EDUCATION" },
    pubDetail: { title: `Books for Mbare library ${T}`, category: "COMMUNITY", summary: "Restock the Mbare community library with school books." },
    pubMedical: { title: `Dialysis for Tapiwa ${T}`, category: "MEDICAL", summary: "Help Tapiwa pay for dialysis sessions this year." },
    pubSuspended: { title: `Suspended fixture campaign ${T}`, category: "EDUCATION" },
    pubUnlisted: { title: `Unlisted fixture campaign ${T}`, category: "EDUCATION" },
    pubDraft: { title: `Draft fixture campaign ${T}`, category: "EDUCATION", cover: false },
  };
  const catalogueSpecs: CampaignSpec[] = Array.from({ length: 13 }, (_, i) => ({
    title: `Catalogue ${token} campaign ${String(i + 1).padStart(2, "0")}`,
    category: i < 7 ? "EDUCATION" : "COMMUNITY",
  }));
  const named = Object.entries(specs);
  const created = await mapLimit([...named.map(([, s]) => s), ...catalogueSpecs], 4, (s) =>
    createReadyCampaign(owner, beneficiaryId, s, (s as { cover?: boolean }).cover !== false),
  );
  const byKey = Object.fromEntries(named.map(([k], i) => [k, created[i]!])) as Record<string, CampaignRef>;
  const catalogue = created.slice(named.length);
  log(`${created.length} campaigns created with scanned covers (${Math.round((Date.now() - t0) / 1000)} s)`);

  // Submit everything except the draft fixture.
  for (const c of created) if (c !== byKey.pubDraft) await submitCampaign(owner, c);

  // linkApprove: under review, assigned to the provisioning reviewer (the linked staff member then tries to approve).
  await provReviewer.post(`/admin/campaigns/${byKey.linkApprove!.id}/assign`, {}, 200);
  await provReviewer.post(`/admin/campaigns/${byKey.linkApprove!.id}/start-review`, {}, 200);

  // Public fixtures: reviewed and published through the API (provisioning, not under test here).
  const toPublish = [byKey.pubDetail!, byKey.pubMedical!, byKey.pubSuspended!, byKey.pubUnlisted!, ...catalogue];
  const published = await mapLimit(toPublish, 1, (c) => approveAndPublish(owner, provReviewer, provCompliance, c));
  for (const p of published) {
    const k = Object.keys(byKey).find((key) => byKey[key]!.id === p.id);
    if (k) byKey[k] = p;
  }
  const catalogueLive = published.slice(4);

  await provReviewer.post(`/admin/campaigns/${byKey.pubSuspended!.id}/suspend`, decision("POLICY_VIOLATION"), 200);
  const unlisted = await campaignStatus(owner, byKey.pubUnlisted!.id);
  await owner.session.patch(`/campaigns/${byKey.pubUnlisted!.id}`, { visibility: "UNLISTED" }, unlisted.etag, 200);
  const draft = await campaignStatus(owner, byKey.pubDraft!.id);
  byKey.pubDraft = { ...byKey.pubDraft!, slug: draft.slug };

  // Updates: one published and visible, one published then hidden by a moderator, one pending moderation
  // (MEDICAL pre-moderates updates).
  const updates = {
    visibleTitle: `Books delivered ${T}`,
    hiddenTitle: `Hidden update ${T}`,
    pendingTitle: `Pending medical update ${T}`,
  };
  const body = "The first boxes of books arrived this week and are already on the shelves.";
  const vis = await owner.session.post(`/campaigns/${byKey.pubDetail!.id}/updates`, { title: updates.visibleTitle, body, publish: true }, 201);
  const hid = await owner.session.post(`/campaigns/${byKey.pubDetail!.id}/updates`, { title: updates.hiddenTitle, body, publish: true }, 201);
  const pend = await owner.session.post(`/campaigns/${byKey.pubMedical!.id}/updates`, { title: updates.pendingTitle, body, publish: true }, 201);
  const st = (r: { data: unknown }) => (r.data as { status: string }).status;
  if (st(vis) !== "PUBLISHED" || st(hid) !== "PUBLISHED") throw new Error(`EDUCATION updates should publish directly: ${st(vis)} ${st(hid)}`);
  if (st(pend) !== "PENDING_MODERATION") throw new Error(`MEDICAL update should be pending moderation: ${st(pend)}`);
  await moderator.post(`/admin/campaigns/updates/${(hid.data as { id: string }).id}/hide`, decision("MISLEADING_CONTENT"), 200);

  const byCategory: Record<string, number> = {};
  for (const c of catalogueLive) byCategory[c.category] = (byCategory[c.category] ?? 0) + 1;

  const run: RunData = {
    runId,
    createdAt: new Date().toISOString(),
    owner: {
      email: owner.email,
      id: owner.id,
      displayName: owner.displayName,
      legalFirstName: legal.first,
      legalLastName: legal.last,
      beneficiaryId,
      session: owner.session.toJSON(),
    },
    linkedStaff,
    staffEmails: { reviewer: S.reviewer!.email, compliance: S.compliance!.email },
    catalogue: { token, campaigns: catalogueLive, byCategory },
    campaigns: byKey as RunData["campaigns"],
    updates,
  };
  saveRun(run);
  saveFixture(fixture);
  log(`run ${runId} provisioned in ${Math.round((Date.now() - t0) / 1000)} s`);
}
