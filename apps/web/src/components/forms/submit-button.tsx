"use client";

import { Button, type ButtonProps } from "@/components/ui/button";

/** Submit button that shows a busy label and is disabled while a request is in flight. */
export function SubmitButton({ busy, busyLabel = "Please wait…", children, ...props }: ButtonProps & { busy: boolean; busyLabel?: string }) {
  return (
    <Button type="submit" disabled={busy} aria-disabled={busy || undefined} {...props}>
      {busy ? busyLabel : children}
    </Button>
  );
}
