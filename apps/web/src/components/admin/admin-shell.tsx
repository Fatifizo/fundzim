import type { ReactNode } from "react";

import { AccountContainer } from "@/components/account/account-shell";
import { AdminBreadcrumbs, TasksIndicator } from "@/components/admin/admin-shell-client";
import { NavLink } from "@/components/layout/nav-link";
import { inputClasses } from "@/components/forms/text-field";
import { buttonClasses } from "@/components/ui/button";
import type { Me } from "@/lib/auth/types";
import { humanise } from "@/lib/payments/labels";
import { can, visibleNav } from "@/lib/operations/navigation";

const linkClass =
  "inline-flex min-h-11 w-full items-center rounded-full px-4 font-medium text-ink-700 hover:bg-brand-50 hover:text-brand-800 aria-[current=page]:bg-brand-50 aria-[current=page]:text-brand-800 aria-[current=page]:underline underline-offset-4";

function StaffNav({ me, id }: { me: Me; id: string }) {
  const groups = visibleNav(me.permissions);
  if (groups.length === 0) return <p className="text-ink-700">Your roles do not include any staff area yet.</p>;
  return (
    <ul className="space-y-4">
      {groups.map((g) => (
        <li key={g.id}>
          <h2 id={`${id}-${g.id}`} className="px-4 text-sm font-semibold uppercase tracking-wide text-ink-600">
            {g.label}
          </h2>
          <ul aria-labelledby={`${id}-${g.id}`} className="mt-1 space-y-1">
            {g.items.map((item) => (
              <li key={item.href}>
                <NavLink href={item.href} exact={item.exact ?? false} prefetch={false} className={linkClass}>
                  {item.label}
                </NavLink>
              </li>
            ))}
          </ul>
        </li>
      ))}
    </ul>
  );
}

/**
 * Staff area chrome (Stage 14): identity, global search, an in-app task indicator, breadcrumbs and navigation built
 * from the caller's effective permissions. Presentation only — every page still checks access itself and every API
 * route enforces its own permission.
 */
export function AdminShell({ me, children }: { me: Me; children: ReactNode }) {
  const dashboard = can(me.permissions, "ops.dashboard.view");
  return (
    <AccountContainer>
      <div className="mb-6 space-y-4">
        <p className="rounded-xl border border-info-700/30 bg-info-50 px-4 py-2 text-sm font-semibold text-info-700">
          Staff area. Everything you open or do here is recorded in the audit log. TEST environment: no live payments, payouts or screening.
        </p>
        <div className="flex flex-col gap-4 rounded-card border border-line bg-surface p-4 lg:flex-row lg:items-center lg:justify-between">
          <div className="min-w-0">
            <p className="font-semibold text-ink-900">
              Signed in as staff: <span className="break-words">{me.display_name}</span>
            </p>
            <p className="text-sm text-ink-600">{me.roles && me.roles.length > 0 ? `Roles: ${me.roles.map(humanise).join(", ")}` : "No active role"}</p>
          </div>
          {dashboard ? (
            <div className="flex flex-col gap-3 sm:flex-row sm:items-center">
              <form role="search" method="get" action="/admin/search" className="flex gap-2">
                <label htmlFor="admin-search" className="sr-only">
                  Search records by id or number
                </label>
                <input id="admin-search" name="q" type="search" minLength={3} maxLength={100} placeholder="Search by id or number" className={`${inputClasses} min-w-0 sm:w-64`} />
                <button type="submit" className={buttonClasses("outline")}>
                  Search
                </button>
              </form>
              <TasksIndicator />
            </div>
          ) : null}
        </div>
        <AdminBreadcrumbs />
      </div>
      <a href="#staff-nav" className="mb-4 inline-flex min-h-11 items-center font-semibold underline underline-offset-4 lg:hidden">
        Go to the staff menu
      </a>
      <div className="grid gap-8 lg:grid-cols-[15rem_1fr]">
        {/* one navigation landmark: after the page content on small screens, in the left column on large ones */}
        <nav id="staff-nav" aria-label="Staff" className="order-last lg:order-first">
          <StaffNav me={me} id="nav" />
        </nav>
        <div className="min-w-0">{children}</div>
      </div>
    </AccountContainer>
  );
}
