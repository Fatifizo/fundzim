"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { Suspense, useEffect, useState } from "react";

import { opsApi, type OpsApi } from "@/lib/operations/api";
import { breadcrumbs } from "@/lib/operations/navigation";
import { obj } from "@/lib/operations/normalise";

function Trail() {
  const pathname = usePathname() ?? "";
  const crumbs = breadcrumbs(pathname);
  if (crumbs.length < 2) return null;
  return (
    <nav aria-label="Breadcrumb">
      <ol className="flex flex-wrap items-center gap-2 text-sm text-ink-600">
        {crumbs.map((c, i) => (
          <li key={`${i}-${c.label}`} className="flex items-center gap-2">
            {i > 0 ? <span aria-hidden="true">/</span> : null}
            {i === crumbs.length - 1 ? (
              <span aria-current="page" className="font-semibold text-ink-900">
                {c.label}
              </span>
            ) : c.href ? (
              <Link href={c.href} prefetch={false} className="underline underline-offset-4">
                {c.label}
              </Link>
            ) : (
              <span>{c.label}</span>
            )}
          </li>
        ))}
      </ol>
    </nav>
  );
}

/** Breadcrumbs from the current path (the pathname is request-time on dynamic routes, so it streams in). */
export function AdminBreadcrumbs() {
  return (
    <Suspense fallback={null}>
      <Trail />
    </Suspense>
  );
}

const TASK_KEYS = ["approvals_to_decide", "support_cases_assigned", "exceptions_assigned", "incidents_owned", "access_review_items_pending"] as const;

/**
 * In-app task indicator (no external notifications in Stage 14): fetched after the page loads so a slow module never
 * delays navigation. A failed fetch says so instead of showing zero.
 */
export function TasksIndicator({ api = opsApi }: { api?: OpsApi }) {
  const [state, setState] = useState<{ total: number } | "loading" | "error">("loading");
  useEffect(() => {
    let live = true;
    api
      .tasks()
      .then((t) => {
        const o = obj(t);
        const total = TASK_KEYS.reduce((n, k) => n + (typeof o[k] === "number" ? (o[k] as number) : 0), 0);
        if (live) setState({ total });
      })
      .catch(() => live && setState("error"));
    return () => {
      live = false;
    };
  }, [api]);
  const label = state === "loading" ? "My tasks (loading)" : state === "error" ? "My tasks (could not load)" : `My tasks: ${state.total}`;
  return (
    <Link href="/admin/operations#my-tasks" prefetch={false} className="inline-flex min-h-11 items-center rounded-full border border-line px-4 font-semibold text-ink-900 underline-offset-4 hover:underline">
      <span aria-live="polite">{label}</span>
    </Link>
  );
}
