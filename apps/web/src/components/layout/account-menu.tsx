"use client";

import Link from "next/link";
import { useEffect, useId, useRef, useState } from "react";

import { SignOutButton } from "@/components/account/sign-out-button";

/**
 * Signed-in account menu (disclosure pattern, like the mobile menu): a button with aria-expanded toggles a
 * panel of links and the sign-out button. Escape closes it and returns focus to the button.
 */
export function AccountMenu({ displayName }: { displayName: string }) {
  const [open, setOpen] = useState(false);
  const panelId = useId();
  const buttonRef = useRef<HTMLButtonElement>(null);
  const containerRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    function onKeyDown(event: KeyboardEvent) {
      if (event.key === "Escape") {
        setOpen(false);
        buttonRef.current?.focus();
      }
    }
    function onPointerDown(event: PointerEvent) {
      if (containerRef.current && !containerRef.current.contains(event.target as Node)) setOpen(false);
    }
    document.addEventListener("keydown", onKeyDown);
    document.addEventListener("pointerdown", onPointerDown);
    return () => {
      document.removeEventListener("keydown", onKeyDown);
      document.removeEventListener("pointerdown", onPointerDown);
    };
  }, [open]);

  const linkClass = "flex min-h-11 items-center rounded-lg px-3 font-medium text-ink-900 hover:bg-brand-50";

  return (
    <div ref={containerRef} className="relative">
      <button
        ref={buttonRef}
        type="button"
        aria-expanded={open}
        aria-controls={panelId}
        onClick={() => setOpen((value) => !value)}
        className="inline-flex min-h-11 max-w-48 items-center gap-2 rounded-full border-2 border-brand-700 px-4 font-semibold text-brand-700 hover:bg-brand-50"
      >
        <span className="sr-only">Account menu for </span>
        <span className="truncate">{displayName}</span>
        <svg aria-hidden="true" viewBox="0 0 20 20" className="size-4 shrink-0" fill="currentColor">
          <path d="M5.5 7.5 10 12l4.5-4.5" stroke="currentColor" strokeWidth="2" fill="none" strokeLinecap="round" />
        </svg>
      </button>
      <div id={panelId} hidden={!open} className="absolute right-0 top-full z-50 mt-2 w-60 rounded-card border border-line bg-surface p-2 shadow-card">
        <nav aria-label="Account">
          <ul>
            <li>
              <Link href="/dashboard" className={linkClass} onClick={() => setOpen(false)}>
                Dashboard
              </Link>
            </li>
            <li>
              <Link href="/settings/profile" className={linkClass} onClick={() => setOpen(false)}>
                Account settings
              </Link>
            </li>
          </ul>
        </nav>
        <div className="mt-2 border-t border-line pt-2">
          <SignOutButton variant="ghost" className="w-full justify-start" />
        </div>
      </div>
    </div>
  );
}
