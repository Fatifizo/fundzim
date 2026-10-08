/**
 * Runs once when a Next.js server instance starts. On the Node.js server it (1) installs the peer-address
 * stamp used for client-IP forwarding (src/lib/net/peer-stamp.ts) and (2) validates server-only
 * configuration, refusing to start in production if it is wrong (see ./instrumentation-node.ts).
 */
export async function register() {
  if (process.env.NEXT_RUNTIME === "nodejs") {
    const { installPeerStamp, validateServerConfig } = await import("./instrumentation-node");
    validateServerConfig();
    installPeerStamp();
  }
}
