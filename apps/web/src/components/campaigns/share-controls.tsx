"use client";

import { useEffect, useId, useState } from "react";

import { Button, buttonClasses } from "@/components/ui/button";
import { trackEvent } from "@/lib/analytics/events";
import { shareLinks, shareText, type ShareChannel } from "@/lib/share/links";

export interface ShareControlsProps {
  slug: string;
  title: string;
  summary: string;
  /** Absolute canonical URL built from PUBLIC_SITE_URL on the server; null when it is not configured. */
  url: string | null;
  /** Non-production builds explain why sharing is missing when the URL is not configured. */
  showConfigNote?: boolean;
}

/**
 * Share a campaign: WhatsApp, Facebook, X and email links, Copy link (announced politely), and the Web Share
 * API as a progressive enhancement. No third-party scripts or widgets. Without a canonical URL every option
 * needs an absolute URL, so none is offered (never guessed from window.location or request headers).
 */
export function ShareControls({ slug, title, summary, url, showConfigNote = false }: ShareControlsProps) {
  const [canShare, setCanShare] = useState(false);
  const [message, setMessage] = useState("");
  const [manualCopy, setManualCopy] = useState(false);
  const manualId = useId();

  useEffect(() => {
    // Feature detection must run in the browser after hydration (navigator does not exist on the server).
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setCanShare(typeof navigator !== "undefined" && typeof navigator.share === "function");
  }, []);

  if (!url) {
    return showConfigNote ? (
      <p className="text-sm text-ink-700">Sharing is turned off on this server: PUBLIC_SITE_URL is not configured, so there is no canonical link to share.</p>
    ) : null;
  }

  const input = { title, summary, url };
  const track = (channel: ShareChannel) => trackEvent({ type: "CAMPAIGN_SHARE_CLICK", slug, channel });

  async function copy() {
    track("COPY_LINK");
    try {
      await navigator.clipboard.writeText(url!);
      setManualCopy(false);
      setMessage("Link copied. You can now paste it anywhere.");
    } catch {
      setManualCopy(true);
      setMessage("Your browser did not allow copying. Select the link below and copy it.");
    }
  }

  async function share() {
    track("WEB_SHARE");
    try {
      await navigator.share({ title, text: shareText(input).replace(/\nRead the story: .*$/, ""), url: url! });
      setMessage("");
    } catch (error) {
      if ((error as { name?: string })?.name !== "AbortError") setMessage("Sharing did not work. Use Copy link instead.");
    }
  }

  return (
    <div className="space-y-3">
      <ul className="grid grid-cols-2 gap-2" aria-label="Share options">
        {shareLinks(input).map((link) => (
          <li key={link.channel}>
            <a
              href={link.href}
              onClick={() => track(link.channel)}
              {...(link.external ? { target: "_blank", rel: "noopener noreferrer" } : {})}
              className={buttonClasses("outline", "md", "w-full px-3")}
            >
              {link.label}
              {link.external ? (
                <>
                  {" "}
                  <span className="sr-only">(opens in a new tab)</span>
                </>
              ) : null}
            </a>
          </li>
        ))}
        <li>
          <Button variant="outline" className="w-full px-3" onClick={() => void copy()}>
            Copy link
          </Button>
        </li>
        {canShare ? (
          <li>
            <Button variant="outline" className="w-full px-3" onClick={() => void share()}>
              More options…
            </Button>
          </li>
        ) : null}
      </ul>
      <p role="status" aria-live="polite" className="min-h-6 text-sm text-ink-700">
        {message}
      </p>
      {manualCopy ? (
        <div className="space-y-1">
          <label htmlFor={manualId} className="block text-sm font-semibold text-ink-900">
            Campaign link
          </label>
          <input id={manualId} readOnly value={url} onFocus={(e) => e.currentTarget.select()} className="min-h-11 w-full rounded-lg border border-line-strong bg-surface px-3 text-ink-900" />
        </div>
      ) : null}
    </div>
  );
}
