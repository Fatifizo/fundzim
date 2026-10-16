"use client";

import { useRouter } from "next/navigation";
import { useEffect, useRef, useState, type FormEvent } from "react";

import { StepUpCancelledError, useStepUp } from "@/components/auth/step-up";
import { FormError, FormStatus } from "@/components/forms/form-error";
import { SelectField, TextAreaField } from "@/components/forms/select-field";
import { SubmitButton } from "@/components/forms/submit-button";
import { TextField } from "@/components/forms/text-field";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { humanise } from "@/lib/payments/labels";
import { describeOpsError } from "@/lib/operations/errors";

/**
 * One staff action in the operations portal: a button that opens a confirmation dialog with its fields, sends the
 * request (stepping up once when the API asks), then refreshes the page. Every action is audited by the API, which
 * is also the authority on whether it is allowed (permission, state, version, separation of duties).
 */
export type ActionField =
  | { kind: "select"; name: string; label: string; options: readonly string[]; placeholder?: string; optional?: boolean }
  | { kind: "text"; name: string; label: string; hint?: string; uuid?: boolean; optional?: boolean }
  | { kind: "textarea"; name: string; label: string; min?: number; max?: number; optional?: boolean };

export interface OpsActionProps {
  button: string;
  title: string;
  description: string;
  confirm: string;
  notice: string;
  fields?: ActionField[];
  /** Fixed body members (e.g. the record version the page was rendered with). */
  body?: Record<string, unknown>;
  run: (body: Record<string, unknown>) => Promise<unknown>;
  variant?: "outline" | "primary";
  /**
   * false keeps the component mounted without its button, so its confirmation survives the refresh that moves the
   * record out of the state where this action applies.
   */
  available?: boolean;
}

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
const len = (s: string) => [...s].length;

export function OpsAction({ button, title, description, confirm, notice, fields = [], body = {}, run, variant = "outline", available = true }: OpsActionProps) {
  const router = useRouter();
  const withStepUp = useStepUp();
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  const [done, setDone] = useState<string | null>(null);
  const noticeRef = useRef<HTMLDivElement>(null);
  const errorRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (done) noticeRef.current?.focus();
  }, [done]);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    const out: Record<string, unknown> = { ...body };
    const errs: Record<string, string> = {};
    for (const f of fields) {
      const v = String(form.get(f.name) ?? "").trim();
      if (!v) {
        if (!f.optional) errs[f.name] = f.kind === "select" ? "Choose an option." : "This is required.";
        continue;
      }
      if (f.kind === "text" && f.uuid && !UUID.test(v)) errs[f.name] = "Paste a full record id.";
      if (f.kind === "textarea" && (len(v) < (f.min ?? 5) || len(v) > (f.max ?? 2000)))
        errs[f.name] = `Write ${f.min ?? 5} to ${(f.max ?? 2000).toLocaleString("en-GB")} characters.`;
      out[f.name] = v;
    }
    setFieldErrors(errs);
    if (Object.keys(errs).length > 0) return;
    setBusy(true);
    setError(null);
    try {
      await withStepUp(() => run(out));
      setOpen(false);
      setDone(notice);
      router.refresh();
    } catch (err) {
      if (!(err instanceof StepUpCancelledError)) {
        const d = describeOpsError(err);
        setError(d.message);
        if (d.field) setFieldErrors({ [d.field]: d.message });
        requestAnimationFrame(() => errorRef.current?.focus());
      }
    } finally {
      setBusy(false);
    }
  }

  if (!available && !done) return null;
  return (
    <div className="space-y-3">
      {done ? <FormStatus ref={noticeRef} title={done} /> : null}
      {available ? (
        <Button
          variant={variant}
          onClick={() => {
            setDone(null);
            setError(null);
            setFieldErrors({});
            setOpen(true);
          }}
        >
          {button}
        </Button>
      ) : null}
      {open && available ? (
        <Dialog title={title} description={description} onClose={() => setOpen(false)}>
          <form noValidate onSubmit={submit} className="space-y-5">
            <FormError ref={errorRef} message={error} />
            {fields.map((f) =>
              f.kind === "select" ? (
                <SelectField
                  key={f.name}
                  label={f.label}
                  name={f.name}
                  placeholderOption={f.placeholder ?? (f.optional ? "None" : "Choose…")}
                  options={f.options.map((o) => ({ value: o, label: humanise(o) }))}
                  error={fieldErrors[f.name]}
                />
              ) : f.kind === "text" ? (
                <TextField key={f.name} label={f.label} name={f.name} autoComplete="off" hint={f.hint} error={fieldErrors[f.name]} />
              ) : (
                <TextAreaField key={f.name} label={f.label} name={f.name} required={!f.optional} maxLength={f.max ?? 2000} error={fieldErrors[f.name]} />
              ),
            )}
            <div className="flex flex-wrap gap-3">
              <SubmitButton busy={busy}>{confirm}</SubmitButton>
              <Button variant="outline" onClick={() => setOpen(false)}>
                Cancel
              </Button>
            </div>
          </form>
        </Dialog>
      ) : null}
    </div>
  );
}
