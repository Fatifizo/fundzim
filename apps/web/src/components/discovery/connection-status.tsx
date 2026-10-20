"use client";

import { useSyncExternalStore } from "react";

function subscribe(onChange: () => void) {
  window.addEventListener("online", onChange);
  window.addEventListener("offline", onChange);
  return () => {
    window.removeEventListener("online", onChange);
    window.removeEventListener("offline", onChange);
  };
}

/**
 * Tells people on unreliable connections why the page stopped responding (Stage 16 poor-connectivity handling). Shown only
 * while the browser reports it is offline; announced politely. Nothing is queued for later: searches, shares and reports
 * need a connection, and no financial or analytics request is ever replayed.
 */
export function ConnectionStatus() {
  const online = useSyncExternalStore(subscribe, () => navigator.onLine, () => true);
  return (
    <div role="status" aria-live="polite" className={online ? "sr-only" : "rounded-xl border border-gold-200 bg-gold-100 p-3 font-semibold text-ink-900"}>
      {online ? "" : "You are offline. What is on this page stays readable; searching, sharing and loading more need a connection."}
    </div>
  );
}
