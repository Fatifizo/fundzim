"use client";

import { Button } from "@/components/ui/button";

/** Opens the browser's print dialog (poster pages). */
export function PrintButton() {
  return <Button onClick={() => window.print()}>Print this poster</Button>;
}
