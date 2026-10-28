import { expect } from "@playwright/test";

import { test } from "../e2e-common/fresh-connections";
import { WEB_URL } from "./support/env";
import { publicGet } from "./support/http";
import { loadRun } from "./support/state";

/**
 * Stage 16 discovery against the REAL stack: the compose worker indexes this run's campaigns from the outbox, search
 * returns the catalogue with marked terms and never a suspended, unlisted or draft campaign, a referral code can be
 * issued and is removed from the address bar after the visit, and the generated preview image follows public
 * visibility.
 */
type Page_ = { items: { slug: string; title: string }[]; total: { value: number }; match: string };

test("search: the catalogue is indexed by the worker; terms are marked; hidden campaigns never appear", async ({ page }) => {
  const r = loadRun();
  const token = r.catalogue.token;
  await expect.poll(async () => (await publicGet<Page_>(`/discovery/search?q=${encodeURIComponent(token)}&limit=48`)).data.total?.value ?? 0, { timeout: 60_000 })
    .toBeGreaterThanOrEqual(r.catalogue.campaigns.length);

  await page.goto(`/campaigns?q=${encodeURIComponent(token)}`);
  const list = page.getByRole("list", { name: "Campaigns" });
  await expect(list.locator(":scope > li").first()).toBeVisible();
  await expect(list.locator("mark").first()).toBeVisible();
  await expect(page.locator('meta[name="robots"]')).toHaveAttribute("content", /noindex/);

  for (const hidden of [r.campaigns.pubSuspended, r.campaigns.pubUnlisted, r.campaigns.pubDraft]) {
    const res = await publicGet<Page_>(`/discovery/search?q=${encodeURIComponent(hidden.title)}&limit=48`);
    expect(res.status).toBe(200);
    expect(res.data.items.map((x) => x.title)).not.toContain(hidden.title);
  }
});

test("referral link: a code is issued for a public campaign and the visit drops it from the address bar", async ({ page, request }) => {
  const c = loadRun().campaigns.pubDetail;
  const res = await request.post(`${WEB_URL}/api/v1/discovery/referrals`, { data: { slug: c.slug, channel: "WHATSAPP" }, headers: { Origin: WEB_URL } });
  expect(res.status()).toBe(201);
  const code = ((await res.json()) as { data: { code: string } }).data.code;
  expect(code).toMatch(/^[0-9a-hjkmnp-tv-z]{12}$/);
  const hidden = await request.post(`${WEB_URL}/api/v1/discovery/referrals`, { data: { slug: loadRun().campaigns.pubSuspended.slug!, channel: "WHATSAPP" }, headers: { Origin: WEB_URL } });
  expect(hidden.status()).toBe(404);

  await page.goto(`/campaigns/${c.slug!}?ref=${code}`);
  await expect(page.getByRole("heading", { level: 1, name: c.title })).toBeVisible();
  await expect(page).toHaveURL(new RegExp(`/campaigns/${c.slug!}$`));
  await expect(page.locator('link[rel="canonical"]')).toHaveAttribute("href", /\/campaigns\/[^?]+$/);
});

test("preview image route follows public visibility", async ({ request }) => {
  const r = loadRun();
  const ok = await request.get(`${WEB_URL}/og/campaigns/${r.campaigns.pubDetail.slug!}`);
  expect(ok.status()).toBe(200);
  expect(ok.headers()["content-type"]).toBe("image/png");
  expect(ok.headers()["cache-control"]).toBe("public, no-cache");
  const gone = await request.get(`${WEB_URL}/og/campaigns/${r.campaigns.pubSuspended.slug!}`);
  expect(gone.status()).toBe(404);
  expect(gone.headers()["cache-control"]).toBe("no-store");
});
