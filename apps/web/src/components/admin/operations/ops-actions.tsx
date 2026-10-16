"use client";

import { OpsAction } from "@/components/admin/operations/ops-action";
import { DeclareIncidentForm } from "@/components/admin/operations/ops-forms";
import { opsApi } from "@/lib/operations/api";
import { humanise } from "@/lib/payments/labels";
import { ESCALATION_TARGETS, INCIDENT_EDGES, REVIEW_DECISIONS, SEVERITIES, SUPPORT_EDGES, TRIAGE_EDGES } from "@/lib/operations/types";

/**
 * The operations portal's own actions (Stage 14): support cases, exception triage, incidents, SLA targets, access
 * reviews, session revocation and role requests. Each is a client wrapper so the request is built here; the API
 * enforces permission, state, version and separation of duties, and audits every one.
 */

const NOTE = { kind: "textarea", name: "note", label: "Note (staff only)" } as const;
const OPTIONAL_NOTE = { ...NOTE, label: "Note (optional, staff only)", optional: true } as const;
const REASON = { kind: "textarea", name: "reason", label: "Reason (staff only)" } as const;

export function TriageActions({ module, exceptionId, status, version, staffId }: { module: string; exceptionId: string; status: string; version: number; staffId?: string }) {
  const next = TRIAGE_EDGES[status] ?? [];
  const run = (b: Record<string, unknown>) => opsApi.triage(module, exceptionId, b);
  return (
    <div className="flex flex-wrap gap-3">
      <OpsAction
        button={status === "UNTRIAGED" ? "Take ownership" : "Reassign"}
        title="Assign this exception?"
        description="Leave the assignee empty to take it yourself. The assignee needs the exception work permission."
        confirm="Confirm: Assign"
        notice="Exception assigned."
        fields={[{ kind: "text", name: "assignee_id", label: "Assignee staff id (optional)", uuid: true, optional: true }, OPTIONAL_NOTE]}
        body={{ version }}
        run={(b) => run({ ...b, assignee_id: (b.assignee_id as string | undefined) ?? staffId })}
      />
      <OpsAction
        available={next.length > 0}
        button="Change triage status"
        title="Change the triage status?"
        description="Resolved is refused while the module still reports the exception; record that you are waiting instead."
        confirm="Confirm: Change status"
        notice="Triage status changed."
        fields={[{ kind: "select", name: "to", label: "New status", options: next }, OPTIONAL_NOTE]}
        body={{ version }}
        run={run}
      />
      <OpsAction button="Add note" title="Add a triage note?" description="Notes are kept in the history and cannot be edited." confirm="Confirm: Add note" notice="Note added." fields={[NOTE]} run={run} />
    </div>
  );
}

export function DeclareFromAlert({ alertKey, title }: { alertKey: string; title: string }) {
  return (
    <details>
      <summary className="min-h-11 cursor-pointer font-semibold underline underline-offset-4">Declare incident</summary>
      <div className="mt-3">
        <DeclareIncidentForm alertKey={alertKey} defaultTitle={title} />
      </div>
    </details>
  );
}

export function SupportActions({ caseId, status, version, canWork }: { caseId: string; status: string; version: number; canWork: boolean }) {
  if (!canWork) return <p className="text-ink-700">You can view this case. Working it needs the support work permission.</p>;
  const next = (SUPPORT_EDGES[status] ?? []).filter((s) => s !== "ESCALATED");
  const canEscalate = (SUPPORT_EDGES[status] ?? []).includes("ESCALATED");
  return (
    <div className="flex flex-wrap gap-3">
      <OpsAction
        button="Assign"
        title="Assign this support case?"
        description="Leave the assignee empty to take it yourself. The assignee needs the support work permission."
        confirm="Confirm: Assign"
        notice="Case assigned."
        fields={[{ kind: "text", name: "assignee_id", label: "Assignee staff id (optional)", uuid: true, optional: true }, OPTIONAL_NOTE]}
        body={{ version }}
        run={(b) => opsApi.supportAction(caseId, "assign", b)}
      />
      <OpsAction
        available={next.length > 0}
        button="Change status"
        title="Change this case's status?"
        description="Resolving or closing needs a resolution note."
        confirm="Confirm: Change status"
        notice="Case status changed."
        fields={[{ kind: "select", name: "to", label: "New status", options: next }, OPTIONAL_NOTE]}
        body={{ version }}
        run={(b) => opsApi.supportAction(caseId, "transition", b)}
      />
      <OpsAction
        available={canEscalate}
        button="Escalate"
        title="Escalate this case?"
        description="The case stays yours: escalation records which team you asked, it does not hand the case over or give you their access."
        confirm="Confirm: Escalate"
        notice="Case escalated."
        fields={[
          { kind: "select", name: "escalated_to", label: "Team", options: ESCALATION_TARGETS },
          { kind: "text", name: "escalation_reference", label: "Their reference (optional)", optional: true },
          NOTE,
        ]}
        body={{ version }}
        run={(b) => opsApi.supportAction(caseId, "escalate", b)}
      />
      <OpsAction
        button="Add note"
        title="Add an internal note?"
        description="Never shown to the requester. No identity numbers, bank details or passwords."
        confirm="Confirm: Add note"
        notice="Note added."
        fields={[{ ...NOTE, max: 4000, min: 3 }]}
        run={(b) => opsApi.supportAction(caseId, "notes", b)}
      />
    </div>
  );
}

export function IncidentActions({ incidentId, status, version, canManage }: { incidentId: string; status: string; version: number; canManage: boolean }) {
  if (!canManage) return <p className="text-ink-700">You can view this incident. Managing it needs the incident management permission.</p>;
  const next = INCIDENT_EDGES[status] ?? [];
  const run = (b: Record<string, unknown>) => opsApi.updateIncident(incidentId, b);
  return (
    <div className="flex flex-wrap gap-3">
      <OpsAction
        available={next.length > 0}
        button="Change status"
        title="Change the incident status?"
        description="Resolving needs a resolution note. A closed incident only takes post-incident notes."
        confirm="Confirm: Change status"
        notice="Incident updated."
        fields={[{ kind: "select", name: "to", label: "New status", options: next }, OPTIONAL_NOTE]}
        body={{ version }}
        run={run}
      />
      <OpsAction
        available={status !== "CLOSED"}
        button="Set owner"
        title="Set the incident owner?"
        description="The owner needs the incident management permission."
        confirm="Confirm: Set owner"
        notice="Owner set."
        fields={[{ kind: "text", name: "owner_id", label: "Owner staff id", uuid: true }, OPTIONAL_NOTE]}
        body={{ version }}
        run={run}
      />
      <OpsAction
        available={status !== "CLOSED"}
        button="Change severity"
        title="Re-rate the severity?"
        description="The change is recorded in the timeline."
        confirm="Confirm: Change severity"
        notice="Severity changed."
        fields={[{ kind: "select", name: "severity", label: "Severity", options: SEVERITIES }, NOTE]}
        body={{ version }}
        run={run}
      />
      <OpsAction
        button={status === "CLOSED" ? "Add post-incident note" : "Add timeline note"}
        title={status === "CLOSED" ? "Add a post-incident note?" : "Add a timeline note?"}
        description="Timeline entries cannot be edited."
        confirm="Confirm: Add note"
        notice="Note added."
        fields={[NOTE]}
        run={run}
      />
    </div>
  );
}

export function SLADecision({ targetId }: { targetId: string }) {
  return (
    <div className="flex flex-wrap gap-3">
      <OpsAction
        button="Approve target"
        title="Approve this internal target?"
        description="Checker step: you cannot approve a target you proposed. It replaces the active target for the metric."
        confirm="Confirm: Approve"
        notice="Target approved."
        fields={[REASON]}
        run={(b) => opsApi.decideSLA(targetId, true, String(b.reason))}
      />
      <OpsAction
        button="Reject target"
        title="Reject this internal target?"
        description="The current target, if any, stays in force."
        confirm="Confirm: Reject"
        notice="Target rejected."
        fields={[REASON]}
        run={(b) => opsApi.decideSLA(targetId, false, String(b.reason))}
      />
    </div>
  );
}

export function ReviewItemDecision({ reviewId, itemId, staffName, role }: { reviewId: string; itemId: string; staffName: string; role: string }) {
  return (
    <OpsAction
      button="Decide"
      title={`Decide ${staffName}'s ${humanise(role)} access?`}
      description="Recommending revocation files a role revocation request: a second person must approve it before the role is removed. You cannot decide your own access."
      confirm="Confirm: Record decision"
      notice="Decision recorded."
      fields={[{ kind: "select", name: "decision", label: "Decision", options: REVIEW_DECISIONS }, REASON]}
      run={(b) => opsApi.decideReviewItem(reviewId, itemId, String(b.decision), String(b.reason))}
    />
  );
}

export function CompleteReview({ reviewId }: { reviewId: string }) {
  return (
    <OpsAction
      button="Complete review"
      title="Complete this access review?"
      description="Only possible once every item is decided."
      confirm="Confirm: Complete"
      notice="Access review completed."
      run={() => opsApi.completeAccessReview(reviewId)}
    />
  );
}

export function RevokeSessions({ staffId, name }: { staffId: string; name: string }) {
  return (
    <OpsAction
      button="End all sessions"
      title={`End all of ${name}'s sessions?`}
      description="They are signed out everywhere immediately. Use this when a staff account may be compromised. Recorded in the security audit log."
      confirm="Confirm: End sessions"
      notice="Sessions ended."
      fields={[REASON]}
      run={(b) => opsApi.revokeSessions(staffId, String(b.reason))}
    />
  );
}

export function RoleRequestDecision({ requestId, action, role }: { requestId: string; action: string; role: string }) {
  const what = `${humanise(action)} ${humanise(role)}`;
  return (
    <div className="flex flex-wrap gap-3">
      <OpsAction
        button="Approve"
        title={`Approve: ${what}?`}
        description="Checker step: you cannot approve a request you made, or one about yourself."
        confirm="Confirm: Approve"
        notice="Role request approved."
        fields={[REASON]}
        run={(b) => opsApi.decideRoleRequest(requestId, true, String(b.reason))}
      />
      <OpsAction
        button="Reject"
        title={`Reject: ${what}?`}
        description="The role stays as it is."
        confirm="Confirm: Reject"
        notice="Role request rejected."
        fields={[REASON]}
        run={(b) => opsApi.decideRoleRequest(requestId, false, String(b.reason))}
      />
    </div>
  );
}
