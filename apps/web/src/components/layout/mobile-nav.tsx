"use client";

import { useEffect, useId, useRef, useState } from "react";

import type { NavItem } from "@/lib/navigation";

import { NavLink } from "./nav-link";

/**
 * Disclosure-pattern mobile menu (WAI-ARIA APG): a button with aria-expanded/aria-controls toggles a
 * list of links. Escape closes it and returns focus to the button; choosing a link closes it.
 */
export function MobileNav({ items }: { items: readonly NavItem[] }) {
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

  return (
    <div ref={containerRef} className="md:hidden">
      <button
        ref={buttonRef}
        type="button"
        aria-expanded={open}
        aria-controls={panelId}
        onClick={() => setOpen((value) => !value)}
        className="inline-flex min-h-11 min-w-11 items-center justify-center gap-2 rounded-full border-2 border-brand-700 px-4 font-semibold text-brand-700"
      >
        <svg aria-hidden="true" viewBox="0 0 24 24" className="size-5" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round">
          {open ? <path d="M6 6l12 12M18 6L6 18" /> : <path d="M4 7h16M4 12h16M4 17h16" />}
        </svg>
        Menu
      </button>
      <nav
        id={panelId}
        aria-label="Main menu"
        hidden={!open}
        className="absolute inset-x-0 top-full border-b border-line bg-surface shadow-card"
      >
        <ul className="mx-auto flex max-w-6xl flex-col px-4 py-2">
          {items.map((item) => (
            <li key={item.href}>
              <NavLink
                href={item.href}
                onClick={() => setOpen(false)}
                className="flex min-h-12 items-center rounded-lg px-3 text-lg font-medium text-ink-900 hover:bg-brand-50 aria-[current=page]:text-brand-700 aria-[current=page]:underline"
              >
                {item.label}
              </NavLink>
            </li>
          ))}
        </ul>
      </nav>
    </div>
  );
}
