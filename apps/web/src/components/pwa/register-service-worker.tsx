"use client";

import { useEffect } from "react";

/**
 * Registers public/sw.js in production builds. The worker caches only hashed /_next/static assets and the
 * offline page; it never caches API responses, documents, media, or authenticated pages (see sw.js).
 */
export function RegisterServiceWorker() {
  useEffect(() => {
    if (process.env.NODE_ENV !== "production" || typeof navigator === "undefined" || !("serviceWorker" in navigator)) return;
    navigator.serviceWorker.register("/sw.js", { scope: "/" }).catch(() => undefined);
  }, []);
  return null;
}
