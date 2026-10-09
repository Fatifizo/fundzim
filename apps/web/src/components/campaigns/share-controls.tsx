"use client";

import { useEffect, useState } from "react";

import { Button } from "@/components/ui/button";

/**
 * Copy-link and native Web Share (when the browser offers it). No third-party scripts or share widgets:
 * the URL is the page's own address without query or fragment. The outcome is announced politely.
 */
export function ShareControls({ title, path }: { title: string; path: string }) {
  const [canShare, setCanShare] = useState(false);
  const [message, setMessage] = useState("");

  useEffect(() => {
    // Feature detection must run in the browser after hydration (navigator does not exist on the server).
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setCanShare(typeof navigator !== "undefined" && typeof navigator.share === "function");
  }, []);

  const url = () => `${window.location.origin}${path}`;

  async function copy() {
    try {
      await navigator.clipboard.writeText(url());
      setMessage("Link copied.");
    } catch {
      setMessage("The link could not be copied. Copy it from your browser's address bar.");
    }
  }

  async function share() {
    try {
      await navigator.share({ title, url: url() });
      setMessage("");
    } catch (error) {
      if ((error as { name?: string })?.name !== "AbortError") setMessage("Sharing did not work. Use Copy link instead.");
    }
  }

  return (
    <div className="space-y-2">
      <div className="flex flex-wrap gap-3">
        <Button variant="outline" onClick={() => void copy()}>
          Copy link
        </Button>
        {canShare ? (
          <Button variant="outline" onClick={() => void share()}>
            Share…
          </Button>
        ) : null}
      </div>
      <p role="status" aria-live="polite" className="min-h-6 text-sm text-ink-700">
        {message}
      </p>
    </div>
  );
}
