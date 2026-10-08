"use client";

import qrcode from "qrcode-generator";
import { useMemo } from "react";

/**
 * Renders a QR code as inline SVG, entirely in the browser (the value never leaves the page, no network,
 * no canvas, no innerHTML). Uses qrcode-generator (MIT, zero dependencies), pinned in package.json.
 */
export function QrCode({ value, label, size = 224 }: { value: string; label: string; size?: number }) {
  const { path, count } = useMemo(() => {
    const qr = qrcode(0, "M");
    qr.addData(value, "Byte");
    qr.make();
    const n = qr.getModuleCount();
    let d = "";
    for (let row = 0; row < n; row++) {
      for (let col = 0; col < n; col++) {
        if (qr.isDark(row, col)) d += `M${col + 4} ${row + 4}h1v1h-1z`;
      }
    }
    return { path: d, count: n };
  }, [value]);

  const box = count + 8; // 4-module quiet zone on each side
  return (
    <svg role="img" aria-label={label} width={size} height={size} viewBox={`0 0 ${box} ${box}`} shapeRendering="crispEdges" className="rounded-xl border border-line bg-white">
      <rect width={box} height={box} fill="#ffffff" />
      <path d={path} fill="#000000" />
    </svg>
  );
}
