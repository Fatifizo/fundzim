// Lab Core Web Vitals for the public pages against a running stack (default http://localhost:3000).
// LAB numbers from Playwright Chromium, not field data. No dependencies beyond playwright-core:
//   - LCP and CLS: PerformanceObserver (largest-contentful-paint, layout-shift with session windows);
//   - INP proxy: Event Timing, max duration of entries with an interactionId (scripted taps + a key press);
//   - TTFB and bytes: Navigation and Resource Timing (transferSize; JS = script resources). Chromium's CDP
//     network emulation delays the body, not responseStart, so TTFB is the server's time to first byte in both
//     profiles (the emulated 150 ms RTT is not included).
// Profiles: mobile = 360×800, 4× CPU throttling, ~1.6 Mbps down / 750 kbps up / 150 ms RTT (CDP);
//           desktop = 1350×940, unthrottled. Cold = new context (empty cache, service workers blocked);
//           warm = reload in the same context. Usage:
//   node scripts/cwv.mjs [--base http://localhost:3000] [--runs 5] [--json out.json]
import { writeFileSync } from "node:fs";
import http from "node:http";
import { deflateSync } from "node:zlib";

import { chromium } from "playwright-core";

const args = Object.fromEntries(process.argv.slice(2).reduce((acc, a, i, all) => (a.startsWith("--") ? [...acc, [a.slice(2), all[i + 1]]] : acc), []));
const BASE = (args.base ?? "http://localhost:3000").replace(/\/$/, "");
const RUNS = Number(args.runs ?? 5);
/**
 * --heavy-images: the development dataset's covers are tiny synthetic PNGs, so image-driven LCP is unrealistically
 * cheap. This serves every public campaign image as a ~150 KB 1200×675 PNG (light noise, so it does not
 * compress away) to approximate a real re-encoded photo. The image comes from a local HTTP server and image
 * requests are re-pointed with route.continue, so they still pass through the throttled network
 * (route.fulfill would bypass Chromium's network emulation).
 */
const HEAVY = args["heavy-images"] !== undefined;
const ONLY = args.only ? new Set(args.only.split(",")) : null;

function heavyPng(W = 1200) {
  const H = Math.round((W * 9) / 16);
  const crcT = new Uint32Array(256).map((_, n) => { let c = n; for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1; return c >>> 0; });
  const crc = (b) => { let c = 0xffffffff; for (const x of b) c = crcT[(c ^ x) & 0xff] ^ (c >>> 8); return (c ^ 0xffffffff) >>> 0; };
  const chunk = (t, d) => { const l = Buffer.alloc(4); l.writeUInt32BE(d.length); const body = Buffer.concat([Buffer.from(t), d]); const c = Buffer.alloc(4); c.writeUInt32BE(crc(body)); return Buffer.concat([l, body, c]); };
  const raw = Buffer.alloc((W * 3 + 1) * H);
  let seed = 7;
  const rnd = () => (((seed = (seed * 1103515245 + 12345) & 0x7fffffff) >>> 16) & 0xff) < 3 ? 1 : 0;
  for (let y = 0; y < H; y++) for (let x = 0; x < W; x++) { const i = y * (W * 3 + 1) + 1 + x * 3; raw[i] = (x >> 3) + rnd(); raw[i + 1] = (y >> 2) + rnd(); raw[i + 2] = 90 + rnd(); }
  const ihdr = Buffer.alloc(13); ihdr.writeUInt32BE(W, 0); ihdr.writeUInt32BE(H, 4); ihdr[8] = 8; ihdr[9] = 2;
  return Buffer.concat([Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]), chunk("IHDR", ihdr), chunk("IDAT", deflateSync(raw, { level: 9 })), chunk("IEND", Buffer.alloc(0))]);
}
/** Full image (no ?w) and one per API width, so srcset choices transfer realistically fewer bytes. */
const HEAVY_BY_WIDTH = HEAVY ? new Map([[0, heavyPng(1200)], ...[320, 640, 1024].map((w) => [w, heavyPng(w)])]) : null;
const HEAVY_PNG = HEAVY_BY_WIDTH?.get(0) ?? null;
let heavyUrl = null;
if (HEAVY_PNG) {
  const server = http.createServer((req, res) => {
    // The original API URL travels in the query; its ?w= picks the variant (absent = the full image).
    const original = decodeURIComponent(new URL(req.url, "http://x").search.slice(1));
    const w = Number(new URL(original, "http://x").searchParams.get("w") ?? 0);
    const body = HEAVY_BY_WIDTH.get(w) ?? HEAVY_PNG;
    const etag = `"heavy-${w}"`;
    if (req.headers["if-none-match"] === etag) {
      res.writeHead(304, { ETag: etag, "Cache-Control": "public, no-cache", "Access-Control-Allow-Origin": "*" });
      res.end();
      return;
    }
    res.writeHead(200, { "Content-Type": "image/png", "Content-Length": body.length, "Cache-Control": "public, no-cache", ETag: etag, "Access-Control-Allow-Origin": "*" });
    res.end(body);
  });
  await new Promise((r) => server.listen(0, "127.0.0.1", r));
  server.unref();
  heavyUrl = `http://127.0.0.1:${server.address().port}/cover.png`;
}

const PROFILES = {
  mobile: { viewport: { width: 360, height: 800 }, isMobile: true, hasTouch: true, deviceScaleFactor: 2, cpu: 4, network: { offline: false, latency: 150, downloadThroughput: (1.6 * 1024 * 1024) / 8, uploadThroughput: (750 * 1024) / 8 } },
  desktop: { viewport: { width: 1350, height: 940 }, isMobile: false, hasTouch: false, deviceScaleFactor: 1, cpu: 1, network: null },
};

/** Installed before any page script: collects LCP, CLS (largest session window) and event durations. */
function observe() {
  const w = window;
  w.__cwv = { lcp: 0, lcpEl: "", cls: 0, events: 0 };
  let session = 0, first = 0, last = 0;
  new PerformanceObserver((list) => {
    for (const e of list.getEntries()) {
      w.__cwv.lcp = e.startTime;
      const el = e.element;
      w.__cwv.lcpEl = el ? `${el.tagName.toLowerCase()}${el.id ? "#" + el.id : ""}${el.tagName === "IMG" ? "[img]" : ""}` : "";
    }
  }).observe({ type: "largest-contentful-paint", buffered: true });
  new PerformanceObserver((list) => {
    for (const e of list.getEntries()) {
      if (e.hadRecentInput) continue;
      if (session && (e.startTime - last > 1000 || e.startTime - first > 5000)) session = 0;
      if (!session) first = e.startTime;
      session += e.value;
      last = e.startTime;
      w.__cwv.cls = Math.max(w.__cwv.cls, session);
    }
  }).observe({ type: "layout-shift", buffered: true });
  new PerformanceObserver((list) => {
    // As INP: only entries that belong to an interaction (tap, click, key press). Hover events such as
    // pointerover, which Playwright's resting pointer fires during load, are not interactions.
    for (const e of list.getEntries()) if (e.interactionId) w.__cwv.events = Math.max(w.__cwv.events, e.duration);
  }).observe({ type: "event", buffered: true, durationThreshold: 16 });
}

async function measure(page, profile) {
  await page.waitForLoadState("networkidle");
  await page.waitForTimeout(1000);
  const before = await page.evaluate(() => {
    const nav = performance.getEntriesByType("navigation")[0];
    const res = performance.getEntriesByType("resource");
    const js = res.filter((r) => r.initiatorType === "script" || /\.js(\?|$)/.test(r.name));
    return {
      lcp: window.__cwv.lcp,
      lcpEl: window.__cwv.lcpEl,
      ttfb: nav.responseStart - nav.startTime,
      bytes: nav.transferSize + res.reduce((n, r) => n + r.transferSize, 0),
      jsBytes: js.reduce((n, r) => n + r.transferSize, 0),
      jsDecoded: js.reduce((n, r) => n + r.decodedBodySize, 0),
      requests: res.length + 1,
    };
  });
  // Interaction (INP proxy): tap the main heading, then the menu button on mobile (a real JS handler) and
  // press Tab. LCP was read first because input ends LCP.
  await page.locator("main h1").first().click({ position: { x: 5, y: 5 } }).catch(() => undefined);
  if (profile === "mobile") {
    const menu = page.getByRole("button", { name: "Menu" });
    if (await menu.isVisible().catch(() => false)) {
      await menu.click();
      await menu.click();
    }
  }
  await page.keyboard.press("Tab");
  await page.waitForTimeout(500);
  const after = await page.evaluate(() => ({ cls: window.__cwv.cls, inp: window.__cwv.events }));
  return { ...before, ...after };
}

async function run(browser, profileName, path) {
  const p = PROFILES[profileName];
  const context = await browser.newContext({ viewport: p.viewport, isMobile: p.isMobile, hasTouch: p.hasTouch, deviceScaleFactor: p.deviceScaleFactor, serviceWorkers: "block" });
  await context.addInitScript(observe);
  if (heavyUrl) await context.route(/\/api\/v1\/public\/campaigns\/[^/]+\/media\//, (route) => route.continue({ url: `${heavyUrl}?${encodeURIComponent(route.request().url())}` }));
  const page = await context.newPage();
  const cdp = await context.newCDPSession(page);
  if (p.cpu > 1) await cdp.send("Emulation.setCPUThrottlingRate", { rate: p.cpu });
  if (p.network) {
    await cdp.send("Network.enable");
    await cdp.send("Network.emulateNetworkConditions", p.network);
  }
  await page.goto(`${BASE}${path}`, { waitUntil: "load", timeout: 120_000 });
  const cold = await measure(page, profileName);
  await page.reload({ waitUntil: "load", timeout: 120_000 });
  const warm = await measure(page, profileName);
  await context.close();
  return { cold, warm };
}

const pct = (values, q) => {
  const s = [...values].sort((a, b) => a - b);
  return s[Math.min(s.length - 1, Math.ceil(q * s.length) - 1)];
};

async function pages() {
  const res = await fetch(`${BASE}/api/v1/public/campaigns?limit=1&category=EDUCATION`);
  const slug = (await res.json()).data.items[0]?.slug;
  if (!slug) throw new Error("no live campaign to measure");
  return { "/": "/", "/campaigns": "/campaigns", "/categories/education": "/categories/education", "campaign detail": `/campaigns/${slug}` };
}

const browser = await chromium.launch();
const targets = await pages();
const rows = [];
const raw = {};
if (HEAVY_BY_WIDTH) console.log(`heavy images (bytes): ${[...HEAVY_BY_WIDTH].map(([w, b]) => `${w || "full"}=${b.length}`).join(", ")}`);
for (const profile of Object.keys(PROFILES).filter((p) => !args.profile || p === args.profile)) {
  for (const [label, path] of Object.entries(targets).filter(([l]) => !ONLY || ONLY.has(l))) {
    const samples = [];
    for (let i = 0; i < RUNS; i++) {
      samples.push(await run(browser, profile, path));
      await new Promise((r) => setTimeout(r, 1500)); // stay well under the API's per-IP limits
    }
    raw[`${profile} ${label}`] = { path, samples };
    for (const mode of ["cold", "warm"]) {
      const s = samples.map((x) => x[mode]);
      const m = (k, q) => pct(s.map((x) => x[k]), q);
      rows.push({
        profile, page: label, mode,
        lcp: [m("lcp", 0.5), m("lcp", 0.95)], cls: [m("cls", 0.5), m("cls", 0.95)], inp: [m("inp", 0.5), m("inp", 0.95)], ttfb: [m("ttfb", 0.5), m("ttfb", 0.95)],
        kb: m("bytes", 0.5) / 1024, jsKb: m("jsBytes", 0.5) / 1024, jsDecodedKb: m("jsDecoded", 0.5) / 1024, requests: m("requests", 0.5), lcpEl: s.map((x) => x.lcpEl).sort().at(-1),
      });
    }
  }
}
await browser.close();

const ms = (v) => `${Math.round(v)}`;
const flag = (ok) => (ok ? "" : " ⚠");
console.log("| Profile | Page | Load | LCP ms p50 / p95 | CLS p50 / p95 | INP-proxy ms p50 / p95 | TTFB ms p50 / p95 | Transfer KB | JS KB (decoded) | Requests | LCP element |");
console.log("|---|---|---|---|---|---|---|---|---|---|---|");
for (const r of rows) {
  console.log(
    `| ${r.profile} | ${r.page} | ${r.mode} | ${ms(r.lcp[0])} / ${ms(r.lcp[1])}${flag(r.lcp[1] <= 2500)} | ${r.cls[0].toFixed(3)} / ${r.cls[1].toFixed(3)}${flag(r.cls[1] <= 0.1)} | ${ms(r.inp[0])} / ${ms(r.inp[1])}${flag(r.inp[1] <= 200)} | ${ms(r.ttfb[0])} / ${ms(r.ttfb[1])} | ${r.kb.toFixed(0)} | ${r.jsKb.toFixed(0)} (${r.jsDecodedKb.toFixed(0)}) | ${r.requests} | ${r.lcpEl} |`,
  );
}
if (args.json) writeFileSync(args.json, JSON.stringify({ base: BASE, runs: RUNS, rows, raw }, null, 2));
