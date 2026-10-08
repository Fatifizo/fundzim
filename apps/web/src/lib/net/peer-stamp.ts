import { randomBytes } from "node:crypto";
import http from "node:http";
import type { IncomingMessage } from "node:http";

import { getPeerHeaderName, PEER_HEADER_PREFIX, setPeerHeaderName } from "./peer-header";

/**
 * Records the real TCP peer address of every incoming HTTP request in a private request header, so Route
 * Handlers and Server Components (which only see headers) can learn who actually connected.
 *
 * Why: Next.js 16 exposes no socket information to Route Handlers. Its only related behaviour is in
 * next/dist/server/base-server.js: `req.headers['x-forwarded-for'] ??= socket.remoteAddress`, i.e. it
 * fills X-Forwarded-For ONLY WHEN THE CLIENT DID NOT SEND ONE. A client-supplied X-Forwarded-For therefore
 * reaches the handler unchanged and is indistinguishable from the peer address — it cannot be trusted.
 *
 * How: wraps `http.Server.prototype.emit` so that, for every 'request' event and before any listener
 * (including Next.js) runs, every header with our prefix is deleted and `<random name>: <peer address>` is
 * set. Runs once per process (idempotent). Node.js only; never imported by Edge/browser code.
 */
export function installPeerStamp(): string {
  const existing = getPeerHeaderName();
  if (existing) return existing;

  const name = `${PEER_HEADER_PREFIX}${randomBytes(16).toString("hex")}`;
  const originalEmit = http.Server.prototype.emit;

  http.Server.prototype.emit = function patchedEmit(this: http.Server, event: string | symbol, ...args: unknown[]) {
    if (event === "request") {
      const req = args[0] as IncomingMessage | undefined;
      if (req && req.headers) stampPeer(req, name);
    }
    return originalEmit.apply(this, [event, ...args] as Parameters<typeof originalEmit>);
  } as typeof originalEmit;

  setPeerHeaderName(name);
  return name;
}

/** Exported for tests. */
export function stampPeer(req: Pick<IncomingMessage, "headers" | "socket">, name: string): void {
  for (const key of Object.keys(req.headers)) {
    if (key.startsWith(PEER_HEADER_PREFIX)) delete req.headers[key];
  }
  const address = req.socket?.remoteAddress;
  if (typeof address === "string" && address !== "") req.headers[name] = address;
}
