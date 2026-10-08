/**
 * Shared, runtime-agnostic accessor for the name of the private request header that carries the TCP peer
 * address observed by this Node.js process (installed by ./peer-stamp.ts from src/instrumentation.ts).
 *
 * The header name is random per process (128 bits), so a client cannot pre-set it: if the stamp is not
 * installed yet (requests that arrive while the server is still starting) the header simply does not exist
 * and callers fall back to "client address unknown". The name never leaves the process: the /api/v1 proxy
 * drops it (and every other `x-fz-peer-*` header) before forwarding.
 */
const KEY = Symbol.for("fundzim.web.peerHeaderName");

export const PEER_HEADER_PREFIX = "x-fz-peer-";

type PeerGlobal = typeof globalThis & { [KEY]?: string };

export function getPeerHeaderName(): string | undefined {
  return (globalThis as PeerGlobal)[KEY];
}

export function setPeerHeaderName(name: string): void {
  (globalThis as PeerGlobal)[KEY] = name;
}

/** Test helper: forget the installed name. */
export function clearPeerHeaderName(): void {
  delete (globalThis as PeerGlobal)[KEY];
}
