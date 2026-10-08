/**
 * Minimal, dependency-free IP address and CIDR handling for the client-address logic of the web tier
 * (src/lib/net/client-ip.ts). Mirrors the API's `net/netip` semantics closely enough for trust decisions:
 *
 *  - IPv4 dotted quads (no leading zeros, no shorthand like `127.1`), IPv6 incl. `::` compression and an
 *    embedded IPv4 tail. Zones (`fe80::1%eth0`), brackets and ports are rejected as malformed.
 *  - IPv4-mapped IPv6 (`::ffff:192.0.2.1`, how Node reports IPv4 peers on dual-stack sockets) is unmapped
 *    to IPv4, so `127.0.0.1/32` matches a `::ffff:127.0.0.1` peer.
 */

export interface ParsedIp {
  version: 4 | 6;
  /** 4 or 16 bytes, network order. */
  bytes: Uint8Array;
}

export interface Cidr {
  version: 4 | 6;
  bytes: Uint8Array;
  prefix: number;
}

const IPV4_OCTET = /^(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)$/;
const HEX_GROUP = /^[0-9a-fA-F]{1,4}$/;

function parseIpv4(text: string): Uint8Array | null {
  const parts = text.split(".");
  if (parts.length !== 4) return null;
  const out = new Uint8Array(4);
  for (let i = 0; i < 4; i++) {
    const part = parts[i]!;
    if (!IPV4_OCTET.test(part)) return null;
    out[i] = Number(part);
  }
  return out;
}

function parseIpv6(text: string): Uint8Array | null {
  if (text.length === 0 || text.length > 45) return null;
  const doubleColon = text.indexOf("::");
  if (doubleColon !== -1 && text.indexOf("::", doubleColon + 1) !== -1) return null;

  const parseGroups = (segment: string): number[] | null => {
    if (segment === "") return [];
    const groups: number[] = [];
    const pieces = segment.split(":");
    for (let i = 0; i < pieces.length; i++) {
      const piece = pieces[i]!;
      if (i === pieces.length - 1 && piece.includes(".")) {
        const v4 = parseIpv4(piece);
        if (!v4) return null;
        groups.push((v4[0]! << 8) | v4[1]!, (v4[2]! << 8) | v4[3]!);
        continue;
      }
      if (!HEX_GROUP.test(piece)) return null;
      groups.push(parseInt(piece, 16));
    }
    return groups;
  };

  let groups: number[];
  if (doubleColon === -1) {
    const all = parseGroups(text);
    if (!all || all.length !== 8) return null;
    groups = all;
  } else {
    const head = parseGroups(text.slice(0, doubleColon));
    const tail = parseGroups(text.slice(doubleColon + 2));
    if (!head || !tail) return null;
    // An embedded IPv4 tail is only valid at the very end.
    if (text.slice(0, doubleColon).includes(".")) return null;
    const missing = 8 - head.length - tail.length;
    if (missing < 1) return null;
    groups = [...head, ...new Array<number>(missing).fill(0), ...tail];
  }

  const out = new Uint8Array(16);
  groups.forEach((group, i) => {
    out[i * 2] = group >> 8;
    out[i * 2 + 1] = group & 0xff;
  });
  return out;
}

function isV4Mapped(bytes: Uint8Array): boolean {
  for (let i = 0; i < 10; i++) if (bytes[i] !== 0) return false;
  return bytes[10] === 0xff && bytes[11] === 0xff;
}

/** Parses a bare IP address. Returns null for anything malformed (ports, brackets, zones, junk). */
export function parseIp(input: string | null | undefined): ParsedIp | null {
  if (typeof input !== "string") return null;
  const text = input.trim();
  if (text === "") return null;
  if (text.includes(":")) {
    const bytes = parseIpv6(text);
    if (!bytes) return null;
    if (isV4Mapped(bytes)) return { version: 4, bytes: bytes.slice(12) };
    return { version: 6, bytes };
  }
  const v4 = parseIpv4(text);
  return v4 ? { version: 4, bytes: v4 } : null;
}

/** Canonical text form (IPv4 dotted quad; IPv6 RFC 5952 compressed lowercase). */
export function formatIp(ip: ParsedIp): string {
  if (ip.version === 4) return Array.from(ip.bytes).join(".");
  const groups: number[] = [];
  for (let i = 0; i < 16; i += 2) groups.push((ip.bytes[i]! << 8) | ip.bytes[i + 1]!);
  // Longest run (≥2) of zero groups, first one wins on ties.
  let bestStart = -1;
  let bestLen = 0;
  for (let i = 0; i < 8; ) {
    if (groups[i] !== 0) {
      i++;
      continue;
    }
    let j = i;
    while (j < 8 && groups[j] === 0) j++;
    if (j - i > bestLen && j - i >= 2) {
      bestStart = i;
      bestLen = j - i;
    }
    i = j;
  }
  const hex = groups.map((g) => g.toString(16));
  if (bestStart === -1) return hex.join(":");
  return `${hex.slice(0, bestStart).join(":")}::${hex.slice(bestStart + bestLen).join(":")}`;
}

/** Parses `addr/prefix` (or a bare address = host route). Returns null when malformed. */
export function parseCidr(input: string): Cidr | null {
  const text = input.trim();
  const slash = text.indexOf("/");
  const addrText = slash === -1 ? text : text.slice(0, slash);
  const ip = parseIp(addrText);
  if (!ip) return null;
  // A mapped address written in IPv6 form keeps IPv6 prefix semantics in the input; normalise to v4.
  const isMappedInput = addrText.includes(":") && ip.version === 4;
  const max = ip.version === 4 ? 32 : 128;
  let prefix = max;
  if (slash !== -1) {
    const prefixText = text.slice(slash + 1);
    if (!/^\d{1,3}$/.test(prefixText)) return null;
    prefix = Number(prefixText);
    if (isMappedInput) {
      if (prefix < 96 || prefix > 128) return null;
      prefix -= 96;
    } else if (prefix > max) {
      return null;
    }
  }
  return { version: ip.version, bytes: ip.bytes, prefix };
}

export function cidrContains(cidr: Cidr, ip: ParsedIp): boolean {
  if (cidr.version !== ip.version) return false;
  let bits = cidr.prefix;
  for (let i = 0; i < cidr.bytes.length && bits > 0; i++) {
    const take = Math.min(8, bits);
    const mask = (0xff << (8 - take)) & 0xff;
    if ((cidr.bytes[i]! & mask) !== (ip.bytes[i]! & mask)) return false;
    bits -= take;
  }
  return true;
}

export class CidrListError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "CidrListError";
  }
}

/** Parses a comma/whitespace-separated CIDR list. Throws CidrListError naming the bad entry. */
export function parseCidrList(input: string | undefined): Cidr[] {
  if (!input || input.trim() === "") return [];
  const entries = input.split(/[\s,]+/).filter(Boolean);
  return entries.map((entry) => {
    const cidr = parseCidr(entry);
    if (!cidr) throw new CidrListError(`invalid CIDR entry: ${JSON.stringify(entry)}`);
    return cidr;
  });
}
