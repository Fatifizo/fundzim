"use client";

import { useEffect, useRef, type ReactNode } from "react";

/**
 * Modal dialog on the native <dialog> element (showModal): the browser provides the focus trap, inert
 * background and Escape. Focus moves to the first form control (or the dialog) on open and returns to the
 * previously focused element on close, as in the Stage 4 step-up dialog.
 */
export function Dialog({ title, description, onClose, children }: { title: string; description?: ReactNode; onClose: () => void; children: ReactNode }) {
  const ref = useRef<HTMLDialogElement>(null);
  const onCloseRef = useRef(onClose);
  useEffect(() => {
    onCloseRef.current = onClose;
  }, [onClose]);

  useEffect(() => {
    const dialog = ref.current;
    if (!dialog) return;
    const previouslyFocused = document.activeElement as HTMLElement | null;
    if (typeof dialog.showModal === "function" && !dialog.open) dialog.showModal();
    else dialog.setAttribute("open", "");
    const first = dialog.querySelector<HTMLElement>("input, select, textarea");
    (first ?? dialog).focus();
    const onCancel = (event: Event) => {
      event.preventDefault();
      onCloseRef.current();
    };
    dialog.addEventListener("cancel", onCancel);
    return () => {
      dialog.removeEventListener("cancel", onCancel);
      if (typeof dialog.close === "function" && dialog.open) dialog.close();
      previouslyFocused?.focus?.();
    };
  }, []);

  return (
    <dialog
      ref={ref}
      tabIndex={-1}
      aria-labelledby="dialog-title"
      aria-describedby={description ? "dialog-description" : undefined}
      className="m-auto max-h-[calc(100%-2rem)] w-[min(36rem,calc(100%-2rem))] overflow-y-auto rounded-card border border-line bg-surface p-6 text-ink-900 shadow-card backdrop:bg-ink-900/60"
    >
      <h2 id="dialog-title" className="font-display text-2xl font-semibold">
        {title}
      </h2>
      {description ? (
        <div id="dialog-description" className="mt-2 text-ink-700">
          {description}
        </div>
      ) : null}
      <div className="mt-5">{children}</div>
    </dialog>
  );
}
