"use client";

import { useEffect, useRef, useState } from "react";

import { Button } from "@/components/ui/button";

/**
 * Shows freshly issued recovery codes ONCE. Codes live only in component state; they are never written to
 * storage, logged or sent anywhere. Copy uses the Clipboard API; download builds a local text file.
 */
export function RecoveryCodes({ codes, onDone }: { codes: string[]; onDone: () => void }) {
  const headingRef = useRef<HTMLHeadingElement>(null);
  const [copied, setCopied] = useState<"idle" | "ok" | "failed">("idle");

  useEffect(() => {
    headingRef.current?.focus();
  }, []);

  async function copy() {
    try {
      await navigator.clipboard.writeText(codes.join("\n"));
      setCopied("ok");
    } catch {
      setCopied("failed");
    }
  }

  function download() {
    const text = `FundZim recovery codes\nEach code works once. Keep them somewhere safe.\n\n${codes.join("\n")}\n`;
    const url = URL.createObjectURL(new Blob([text], { type: "text/plain" }));
    const link = document.createElement("a");
    link.href = url;
    link.download = "fundzim-recovery-codes.txt";
    document.body.appendChild(link);
    link.click();
    link.remove();
    setTimeout(() => URL.revokeObjectURL(url), 0);
  }

  return (
    <section aria-labelledby="recovery-codes-title" className="space-y-4 rounded-card border border-gold-500/50 bg-gold-100 p-5">
      <h2 id="recovery-codes-title" ref={headingRef} tabIndex={-1} className="font-display text-xl font-semibold text-ink-900 focus:outline-none">
        Save your recovery codes
      </h2>
      <p className="text-ink-700">
        If you lose access to your authenticator app, each of these codes lets you sign in once. They will not be
        shown again. Store them somewhere safe, such as a password manager.
      </p>
      <ol aria-label="Recovery codes" className="grid grid-cols-1 gap-2 font-mono text-lg sm:grid-cols-2">
        {codes.map((code) => (
          <li key={code} className="rounded-lg bg-surface px-3 py-2 tracking-wider text-ink-900">
            {code}
          </li>
        ))}
      </ol>
      <div className="flex flex-wrap items-center gap-3">
        <Button variant="outline" onClick={copy}>
          Copy codes
        </Button>
        <Button variant="outline" onClick={download}>
          Download as text file
        </Button>
        <Button onClick={onDone}>I have saved my codes</Button>
      </div>
      <p role="status" className="text-sm text-ink-700">
        {copied === "ok" ? "Codes copied to the clipboard." : copied === "failed" ? "Copy failed. Select the codes and copy them manually." : ""}
      </p>
    </section>
  );
}
