"use client";

import { useState } from "react";

import { cx } from "@/lib/cx";

/**
 * Same-origin campaign image with a quiet fallback: if the API answers 404 (for example media removed after
 * approval), a neutral block is shown instead of a broken-image icon. Plain <img>: next/image emits inline
 * styles that the strict CSP blocks.
 */
export function PublicImage({ src, alt, width, height, className, priority = false }: { src: string; alt: string; width: number; height: number; className?: string; priority?: boolean }) {
  const [failed, setFailed] = useState(false);
  if (failed) return <div aria-hidden="true" className={cx(className, "bg-[linear-gradient(135deg,var(--color-brand-100),var(--color-gold-200))]")} />;
  return (
    // eslint-disable-next-line @next/next/no-img-element
    <img src={src} alt={alt} width={width} height={height} loading={priority ? "eager" : "lazy"} fetchPriority={priority ? "high" : "auto"} onError={() => setFailed(true)} className={className} />
  );
}
