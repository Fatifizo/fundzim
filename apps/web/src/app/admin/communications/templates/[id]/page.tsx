import type { Metadata } from "next";

import { AccountPage } from "@/components/account/account-shell";
import { Code, Facts, Section, When } from "@/components/admin/payments/common";
import { NoPermission, ShortId } from "@/components/admin/operations/ops";
import { CommsBanner } from "@/components/admin/communications/comms";
import { DraftForm, PreviewButton, TemplateVersionActions } from "@/components/admin/communications/comms-actions";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { humanise } from "@/lib/payments/labels";
import { can } from "@/lib/operations/navigation";
import { obj } from "@/lib/operations/normalise";
import { templateActions, type Template } from "@/lib/notifications/types";

export const metadata: Metadata = { title: "Notification template", robots: { index: false, follow: false, nocache: true } };

/** One template's versions. Drafting needs `communications.template.request`; deciding `communications.template.approve` (a different person). */
export default async function TemplatePage({ params }: PageProps<"/admin/communications/templates/[id]">) {
  const me = await requireStaff();
  const { id } = await params;
  const t = obj((await serverGetOr404<unknown>(`/api/v1/admin/communications/templates/${encodeURIComponent(id)}`, `/admin/communications/templates/${encodeURIComponent(id)}`)).data) as unknown as Template;
  const versions = t.versions ?? [];
  const active = versions.find((v) => v.status === "ACTIVE") ?? null;
  const open = versions.some((v) => ["DRAFT", "PENDING_APPROVAL", "APPROVED"].includes(v.status));
  const maker = can(me.permissions, "communications.template.request");
  const checker = can(me.permissions, "communications.template.approve");
  return (
    <AccountPage title={t.key} intro={`${humanise(t.category)} · allowed variables: ${["display_name", ...t.allowed_variables].join(", ")}`}>
      <CommsBanner />
      {versions.map((v) => {
        const mine = v.created_by === me.id || v.submitted_by === me.id;
        const allowed = templateActions(v.status).filter((a) => (a === "submit" ? maker && v.created_by === me.id : checker && !mine));
        if (maker && v.whatsapp_template_name && !["SIMULATOR_ACCEPTED", "APPROVED"].includes(v.whatsapp_provider_status)) allowed.push("whatsapp-simulator");
        return (
          <Section key={v.id} id={`v${v.version_no}`} title={`Version ${v.version_no} — ${humanise(v.status)}`}>
            <Facts items={[
              ["In-app", `${v.inapp_title} — ${v.inapp_body}`], ["E-mail", v.email_subject ? `${v.email_subject}\n\n${v.email_text ?? ""}` : "None"],
              ["SMS", v.sms_text ? `${v.sms_text} (${v.sms_segments} segment${v.sms_segments === 1 ? "" : "s"})` : "None"],
              ["WhatsApp", v.whatsapp_template_name ? `${v.whatsapp_template_name} (${v.whatsapp_language}) — ${humanise(v.whatsapp_provider_status)}` : "None"],
              ["Approval", v.approval_source === "MIGRATION_BASELINE" ? "Baseline (existing notice text)" : v.approved_by ? <ShortId key="a" value={v.approved_by} /> : "—"],
              ["Made by", v.created_by ? <ShortId key="m" value={v.created_by} /> : "Migration"], ["Content hash", <Code key="h">{v.content_hash.slice(0, 16)}…</Code>],
              ["Created", <When key="c" at={v.created_at} />],
            ]} />
            <div className="flex flex-wrap items-start gap-3">
              <PreviewButton versionId={v.id} />
              {allowed.length > 0 ? <TemplateVersionActions version={v} actions={allowed} /> : null}
            </div>
            {mine && ["PENDING_APPROVAL", "APPROVED"].includes(v.status) ? <p className="text-sm text-ink-600">You made or submitted this version: a different person decides it.</p> : null}
          </Section>
        );
      })}
      <Section id="draft" title="Draft a new version">
        {open ? <p className="text-ink-700">A version is already open. Finish or retire it first.</p> : maker ? (
          <DraftForm templateId={t.id} base={active} allowed={t.allowed_variables} />
        ) : (
          <NoPermission action="Drafting a version" permission="communications.template.request" />
        )}
      </Section>
    </AccountPage>
  );
}
