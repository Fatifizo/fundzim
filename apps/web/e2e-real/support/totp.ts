import { createHmac } from "node:crypto";
import fs from "node:fs";
import path from "node:path";

import { STATE_DIR } from "./env";

/**
 * RFC 6238 TOTP (HMAC-SHA1, 6 digits, 30 s) implemented with node:crypto.
 *
 * The API refuses a replayed time step for the same secret, and global setup and the test workers are
 * separate processes, so the last step used per secret is kept in a small file (.state/totp-steps.json).
 * A code is only ever produced for a step newer than the last one used; when the ±1 window is exhausted
 * the caller waits for the next step.
 */
const STEP_SECONDS = 30;
const STEPS_FILE = path.join(STATE_DIR, "totp-steps.json");

function base32Decode(input: string): Buffer {
  const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567";
  const clean = input.toUpperCase().replace(/=+$/, "").replace(/\s+/g, "");
  let bits = 0;
  let value = 0;
  const out: number[] = [];
  for (const ch of clean) {
    const idx = alphabet.indexOf(ch);
    if (idx < 0) throw new Error("TOTP secret is not base32");
    value = (value << 5) | idx;
    bits += 5;
    if (bits >= 8) {
      out.push((value >>> (bits - 8)) & 0xff);
      bits -= 8;
    }
  }
  return Buffer.from(out);
}

/** HOTP (RFC 4226) value for a counter. */
export function hotp(secret: string, counter: number, digits = 6): string {
  const msg = Buffer.alloc(8);
  msg.writeBigUInt64BE(BigInt(counter));
  const mac = createHmac("sha1", base32Decode(secret)).update(msg).digest();
  const offset = mac[mac.length - 1]! & 0x0f;
  const bin = ((mac[offset]! & 0x7f) << 24) | (mac[offset + 1]! << 16) | (mac[offset + 2]! << 8) | mac[offset + 3]!;
  return String(bin % 10 ** digits).padStart(digits, "0");
}

function readSteps(): Record<string, number> {
  try {
    return JSON.parse(fs.readFileSync(STEPS_FILE, "utf8")) as Record<string, number>;
  } catch {
    return {};
  }
}

function writeSteps(steps: Record<string, number>) {
  fs.mkdirSync(STATE_DIR, { recursive: true });
  const tmp = `${STEPS_FILE}.${process.pid}.tmp`;
  fs.writeFileSync(tmp, JSON.stringify(steps), { mode: 0o600 });
  fs.renameSync(tmp, STEPS_FILE);
}

/** Key for the step file: never the secret itself. */
function keyOf(secret: string) {
  return createHmac("sha256", "fundzim-e2e-step-key").update(secret).digest("hex").slice(0, 24);
}

/**
 * A valid code for a time step not used before for this secret: the current step or the next one (the
 * server accepts ±1 step); waits for a new step when both were used.
 */
export async function totpCode(secret: string): Promise<string> {
  const key = keyOf(secret);
  for (;;) {
    const steps = readSteps();
    const last = steps[key] ?? 0;
    const cur = Math.floor(Date.now() / 1000 / STEP_SECONDS);
    // The current step, else the next one (inside the server's ±1 window); otherwise wait a step.
    for (const s of [cur, cur + 1]) {
      if (s > last) {
        steps[key] = s;
        writeSteps(steps);
        return hotp(secret, s);
      }
    }
    const waitMs = ((cur + 1) * STEP_SECONDS * 1000 - Date.now()) + 1000;
    await new Promise((r) => setTimeout(r, Math.max(waitMs, 1000)));
  }
}
