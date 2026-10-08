import type { VersionData } from "@/lib/api/types";

export type ApiStatusResult =
  | { state: "up"; version: VersionData | null }
  | { state: "down"; code: string; requestId?: string };

/** Presentational part of ApiStatus (no server-only imports, unit-testable). */
export function ApiStatusView({ status }: { status: ApiStatusResult }) {
  if (status.state === "up") {
    const v = status.version;
    return (
      <p className="text-sm text-brand-100">
        <span aria-hidden="true" className="mr-2 inline-block size-2 rounded-full bg-gold-400" />
        Platform API: reachable
        {v ? (
          <>
            {" "}
            · version {v.version} ({v.commit.slice(0, 12)})
          </>
        ) : null}
      </p>
    );
  }
  return (
    <p className="text-sm text-brand-100">
      <span aria-hidden="true" className="mr-2 inline-block size-2 rounded-full border border-brand-100" />
      Platform API: not reachable right now
      {status.requestId ? <> · ref {status.requestId}</> : null}
    </p>
  );
}
