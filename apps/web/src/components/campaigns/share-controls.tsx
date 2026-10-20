"use client";

import { useEffect, useId, useRef, useState } from "react";

import { Button, buttonClasses } from "@/components/ui/button";
import { trackEvent } from "@/lib/analytics/events";
import { referralCode } from "@/lib/share/referral";
import { shareLinks, shareText, withReferral, type ShareChannel, type ShareInput, type ShareLink } from "@/lib/share/links";

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
 * Share a campaign: WhatsApp, Facebook, X, LinkedIn and email links, Copy link (announced politely) and the Web Share
 * API as a progressive enhancement. No third-party scripts or widgets: plain links that open the other service. Sharing
 * is always user-initiated; FundZim never sends a message on anyone's behalf.
 *
 * Each share asks the API for a referral code (campaign + channel only, no personal data) and appends it as `?ref=`, so
 * the organiser can see which channels bring visitors. The code request is bounded (800 ms) and optional: without it the
 * plain canonical URL is shared. External services open in a window created synchronously in the click (popup
 * blockers allow it), with the opener cut before navigating.
 */
export function ShareControls({ slug, title, summary, url, showConfigNote = false }: ShareControlsProps) {
  const [canShare, setCanShare] = useState(false);
  const [message, setMessage] = useState("");
  const [manualCopy, setManualCopy] = useState(false);
  const [manualUrl, setManualUrl] = useState("");
  const busy = useRef(false);
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
  const canonical = url;
  const input: ShareInput = { title, summary, url: canonical };

  async function sharedUrl(channel: ShareChannel): Promise<string> {
    return withReferral(canonical, await referralCode(slug, channel));
  }

  async function openExternal(event: React.MouseEvent<HTMLAnchorElement>, link: ShareLink) {
    trackEvent({ type: "SHARE_CLICK", slug, channel: link.channel });
    if (!link.external) {
      // mailto: no new window; resolve the referral first, then navigate
      event.preventDefault();
      const u = await sharedUrl(link.channel);
      const target = shareLinks({ ...input, url: u }).find((l) => l.channel === link.channel)?.href ?? link.href;
      window.location.assign(target);
      return;
    }
    // modifier clicks keep the browser's own behaviour (the plain link)
    if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey || event.button !== 0) return;
    event.preventDefault();
    const win = window.open("about:blank", "_blank");
    if (!win) {
      window.location.assign(link.href); // a blocked popup: follow the plain link in this tab
      return;
    }
    try {
      win.opener = null;
    } catch {
      // ignore
    }
    const u = await sharedUrl(link.channel);
    const target = shareLinks({ ...input, url: u }).find((l) => l.channel === link.channel)?.href ?? link.href;
    win.location.replace(target);
  }

  async function copy() {
    if (busy.current) return;
    busy.current = true;
    trackEvent({ type: "SHARE_CLICK", slug, channel: "COPY_LINK" });
    const u = await sharedUrl("COPY_LINK");
    try {
      await navigator.clipboard.writeText(u);
      setManualCopy(false);
      setMessage("Link copied. You can now paste it anywhere.");
    } catch {
      setManualUrl(u);
      setManualCopy(true);
      setMessage("Your browser did not allow copying. Select the link below and copy it.");
    } finally {
      busy.current = false;
    }
  }

  async function share() {
    if (busy.current) return;
    busy.current = true;
    trackEvent({ type: "SHARE_CLICK", slug, channel: "WEB_SHARE" });
    try {
      const u = await sharedUrl("WEB_SHARE");
      await navigator.share({ title, text: shareText({ ...input, url: u }).replace(/\nRead the story: .*$/, ""), url: u });
      setMessage("");
    } catch (error) {
      if ((error as { name?: string })?.name !== "AbortError") setMessage("Sharing did not work. Use Copy link instead.");
    } finally {
      busy.current = false;
    }
  }

  return (
    <div className="space-y-3">
      <ul className="grid grid-cols-2 gap-2" aria-label="Share options">
        {shareLinks(input).map((link) => (
          <li key={link.channel}>
            <a
              href={link.href}
              onClick={(e) => void openExternal(e, link)}
              {...(link.external ? { target: "_blank", rel: "noopener noreferrer" } : {})}
              className={buttonClasses(link.channel === "WHATSAPP" ? "primary" : "outline", "md", "w-full px-3")}
              data-channel={link.channel}
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
          <input id={manualId} readOnly value={manualUrl || canonical} onFocus={(e) => e.currentTarget.select()} className="min-h-11 w-full rounded-lg border border-line-strong bg-surface px-3 text-ink-900" />
        </div>
      ) : null}
      <p className="text-xs text-ink-600">
        Shared links carry a short code that tells the organiser which app brought visitors. It does not identify you or anyone you share with.
      </p>
    </div>
  );
}
