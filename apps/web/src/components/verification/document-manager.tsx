"use client";

import { useCallback, useEffect, useId, useRef, useState, type FormEvent } from "react";

import { formatDateTime } from "@/components/account/format";
import { useStepUp } from "@/components/auth/step-up";
import { FormError } from "@/components/forms/form-error";
import { SelectField } from "@/components/forms/select-field";
import { SubmitButton } from "@/components/forms/submit-button";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { browserNavigation } from "@/lib/browser-navigation";
import { cx } from "@/lib/cx";
import { verificationApi, type VerificationApi } from "@/lib/verification/api";
import { describeVerificationError } from "@/lib/verification/errors";
import { documentTypeLabel, documentTypeOptions, normaliseRequirements, requirementLabel, scanStatusLabel, sideLabel, sidesFor } from "@/lib/verification/labels";
import { PENDING_SCAN_STATUSES, type DocumentRequirement, type SubjectType, type VerificationDocument } from "@/lib/verification/types";
import { ACCEPT_ATTRIBUTE, checkFile, formatBytes, safeDocumentUrl, uploadDocument, type UploadInput, type UploadOptions } from "@/lib/verification/upload";

import { useVerificationFeedback } from "./use-verification-feedback";

/** Backoff for scan-status polling: 1.5 s doubling to 20 s, giving up after ~10 minutes. */
export const POLL_DELAYS_MS = [1500, 3000, 6000, 12000, 20000];
const POLL_MAX_MS = 10 * 60_000;

export interface DocumentManagerProps {
  subjectType: SubjectType;
  subjectId: string;
  initialDocuments: VerificationDocument[];
  requirements?: DocumentRequirement[];
  editable: boolean;
  heading?: string;
  /** Heading level for the section title (default 2). */
  level?: 2 | 3;
  description?: string;
  /** Anchor id for error-summary links (`field-<anchor>`). */
  anchor?: string;
  onDocumentsChange?: (documents: VerificationDocument[]) => void;
  api?: VerificationApi;
  upload?: (input: UploadInput, options: UploadOptions) => Promise<VerificationDocument>;
  pollDelaysMs?: number[];
}

function visible(documents: VerificationDocument[]) {
  return documents.filter((d) => d.status !== "DELETED");
}

export function DocumentManager({
  subjectType,
  subjectId,
  initialDocuments,
  requirements: rawRequirements = [],
  editable,
  heading = "Documents",
  level = 2,
  description,
  anchor = "documents",
  onDocumentsChange,
  api = verificationApi,
  upload = uploadDocument,
  pollDelaysMs = POLL_DELAYS_MS,
}: DocumentManagerProps) {
  const withStepUp = useStepUp();
  const requirements = normaliseRequirements(rawRequirements);
  const [documents, setDocuments] = useState<VerificationDocument[]>(() => visible(initialDocuments));
  const typeOptions = documentTypeOptions(subjectType, requirements);
  const [documentType, setDocumentType] = useState<string>(typeOptions[0] ?? "OTHER");
  const sides = sidesFor(documentType, requirements);
  const [progress, setProgress] = useState<{ loaded: number; total: number } | null>(null);
  const [announcement, setAnnouncement] = useState("");
  const [pendingDelete, setPendingDelete] = useState<VerificationDocument | null>(null);
  const [busyId, setBusyId] = useState<string | null>(null);
  const [listError, setListError] = useState<string | null>(null);
  const abortRef = useRef<AbortController | null>(null);
  const fileRef = useRef<HTMLInputElement>(null);
  const listErrorRef = useRef<HTMLDivElement>(null);
  const { formError, fieldErrors, errorRef, formRef, clear, fail, failWith } = useVerificationFeedback();
  const headingId = useId();
  const Heading = `h${level}` as const;

  const documentsRef = useRef(documents);
  useEffect(() => {
    documentsRef.current = documents;
  }, [documents]);
  const onChangeRef = useRef(onDocumentsChange);
  useEffect(() => {
    onChangeRef.current = onDocumentsChange;
  }, [onDocumentsChange]);
  useEffect(() => {
    onChangeRef.current?.(documents);
  }, [documents]);

  // ---- scan-status polling with backoff ----
  const pendingIds = documents.filter((d) => PENDING_SCAN_STATUSES.has(d.status)).map((d) => d.id).join(",");
  useEffect(() => {
    if (!pendingIds) return;
    let cancelled = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const started = Date.now();
    let attempt = 0;
    const tick = async () => {
      if (cancelled) return;
      for (const id of pendingIds.split(",")) {
        try {
          const fresh = await api.getDocument(id);
          if (cancelled) return;
          const before = documentsRef.current.find((doc) => doc.id === id);
          if (!before || before.status === fresh.status) continue;
          setAnnouncement(`${documentTypeLabel(fresh.document_type)}${sideLabel(fresh.side) ? ` (${sideLabel(fresh.side).toLowerCase()})` : ""}: ${scanStatusLabel(fresh.status).label}.`);
          setDocuments((current) => visible(current.map((doc) => (doc.id === id ? fresh : doc))));
        } catch {
          // Transient read failure: try again on the next tick.
        }
      }
      if (cancelled || Date.now() - started > POLL_MAX_MS) return;
      const delay = pollDelaysMs[Math.min(attempt, pollDelaysMs.length - 1)]!;
      attempt += 1;
      timer = setTimeout(tick, delay);
    };
    timer = setTimeout(tick, pollDelaysMs[0]);
    return () => {
      cancelled = true;
      if (timer) clearTimeout(timer);
    };
  }, [pendingIds, api, pollDelaysMs]);

  useEffect(() => () => abortRef.current?.abort(), []);

  const reload = useCallback(async () => {
    try {
      setDocuments(visible(await api.listDocuments(subjectType, subjectId)));
    } catch {
      // The list on screen stays; the next action will report problems.
    }
  }, [api, subjectType, subjectId]);

  async function onUpload(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = event.currentTarget;
    const data = new FormData(form);
    // Read the File from the input itself (FormData of a file input is not reliable across environments).
    const file = fileRef.current?.files?.[0];
    const side = String(data.get("side") ?? "");
    if (!file) {
      fail("Please correct the errors below.", { file: "Choose a file to upload." });
      return;
    }
    const problem = checkFile(file);
    if (problem) {
      fail("Please correct the errors below.", { file: fileProblemMessage(problem) });
      return;
    }
    if (sides.length > 0 && !side) {
      fail("Please correct the errors below.", { side: "Choose which side of the document this is." });
      return;
    }
    clear();
    const controller = new AbortController();
    abortRef.current = controller;
    setProgress({ loaded: 0, total: file.size });
    setAnnouncement(`Uploading ${file.name}.`);
    try {
      const doc = await upload(
        { file, subjectType, subjectId, documentType, side: side || undefined },
        { signal: controller.signal, onProgress: (loaded, total) => setProgress({ loaded, total }) },
      );
      setDocuments((current) => [...current.filter((d) => d.id !== doc.id), doc]);
      setAnnouncement(`${documentTypeLabel(doc.document_type)} uploaded. ${scanStatusLabel(doc.status).label}.`);
      form.reset();
      setDocumentType(typeOptions[0] ?? "OTHER");
    } catch (error) {
      const described = failWith(error, "The upload did not complete. Please try again.", { file: "file" });
      if (described?.code === "TIMEOUT") void reload();
      setAnnouncement("");
    } finally {
      abortRef.current = null;
      setProgress(null);
    }
  }

  async function onView(doc: VerificationDocument) {
    setListError(null);
    setBusyId(doc.id);
    try {
      const access = await withStepUp(() => api.documentAccess(doc.id));
      const url = safeDocumentUrl(access.url, doc.id);
      if (!url) throw new Error("unexpected document URL");
      // The content endpoint answers as an attachment: the browser downloads it and stays on this page.
      browserNavigation.assign(url);
    } catch (error) {
      const described = describeVerificationError(error, "We couldn't open this document. Please try again.");
      if ((error as Error)?.name !== "StepUpCancelledError") {
        setListError(described.message);
        requestAnimationFrame(() => listErrorRef.current?.focus());
      }
    } finally {
      setBusyId(null);
    }
  }

  async function confirmDelete() {
    const doc = pendingDelete;
    if (!doc) return;
    setPendingDelete(null);
    setListError(null);
    setBusyId(doc.id);
    try {
      await api.deleteDocument(doc.id);
      setDocuments((current) => current.filter((d) => d.id !== doc.id));
      setAnnouncement(`${documentTypeLabel(doc.document_type)} deleted.`);
    } catch (error) {
      setListError(describeVerificationError(error, "We couldn't delete this document. Please try again.").message);
      requestAnimationFrame(() => listErrorRef.current?.focus());
    } finally {
      setBusyId(null);
    }
  }

  const percent = progress && progress.total > 0 ? Math.min(100, Math.floor((progress.loaded * 100) / progress.total)) : 0;

  return (
    <section aria-labelledby={headingId} className="space-y-4">
      <Heading id={headingId} className={cx("font-display font-semibold text-ink-900", level === 2 ? "text-xl" : "text-lg")}>
        {heading}
      </Heading>
      {description ? <p className="text-ink-700">{description}</p> : null}
      <p className="sr-only" role="status" aria-live="polite">
        {announcement}
      </p>

      {requirements.length > 0 ? (
        <div id={`field-${anchor}`} tabIndex={-1} className="focus:outline-none">
          <h3 className="font-semibold text-ink-900">What we need</h3>
          <ul className="mt-2 space-y-1">
            {requirements.map((req) => {
              const ready = req.satisfied || requirementReady(req, documents);
              return (
                <li key={(req.document_types ?? [req.document_type]).join("|")} className="flex flex-wrap items-center gap-2 text-ink-700">
                  <span>{requirementLabel(req)}</span>
                  {ready ? <Badge tone="brand">Provided</Badge> : <Badge tone="gold">Needed</Badge>}
                </li>
              );
            })}
          </ul>
        </div>
      ) : null}

      <FormError ref={listErrorRef} message={listError} />

      {documents.length === 0 ? (
        <p className="text-ink-700">No documents uploaded yet.</p>
      ) : (
        <ul className="divide-y divide-line rounded-xl border border-line bg-surface" aria-label={`${heading}: uploaded files`}>
          {documents.map((doc) => {
            const status = scanStatusLabel(doc.status);
            return (
              <li key={doc.id} className="flex flex-col gap-2 p-4 sm:flex-row sm:items-start sm:justify-between">
                <div className="min-w-0 space-y-1">
                  <p className="font-semibold text-ink-900">
                    {documentTypeLabel(doc.document_type)}
                    {sideLabel(doc.side) ? ` — ${sideLabel(doc.side)}` : ""}
                  </p>
                  <p className="flex flex-wrap items-center gap-2 text-sm text-ink-600">
                    <Badge tone={status.tone}>{status.label}</Badge>
                    <span>{formatBytes(doc.size_bytes)}</span>
                    <span>Uploaded {formatDateTime(doc.uploaded_at)}</span>
                  </p>
                  <p className="text-sm text-ink-700">{status.description}</p>
                  {doc.status === "REJECTED" && doc.rejected_reason ? (
                    <p className="text-sm font-semibold text-danger-700">Reason: {humaniseReason(doc.rejected_reason)}</p>
                  ) : null}
                </div>
                <div className="flex shrink-0 flex-wrap gap-2">
                  {doc.status === "CLEAN" ? (
                    <Button variant="outline" disabled={busyId === doc.id} onClick={() => onView(doc)} aria-label={`View ${documentTypeLabel(doc.document_type)}${sideLabel(doc.side) ? ` ${sideLabel(doc.side).toLowerCase()}` : ""}`}>
                      View
                    </Button>
                  ) : null}
                  {editable ? (
                    <Button variant="ghost" disabled={busyId === doc.id} onClick={() => setPendingDelete(doc)} aria-label={`Delete ${documentTypeLabel(doc.document_type)}${sideLabel(doc.side) ? ` ${sideLabel(doc.side).toLowerCase()}` : ""}`}>
                      Delete
                    </Button>
                  ) : null}
                </div>
              </li>
            );
          })}
        </ul>
      )}

      {editable ? (
        <form ref={formRef} noValidate onSubmit={onUpload} className="space-y-4 rounded-xl border border-line bg-surface-muted p-4" aria-label={`Upload to ${heading}`}>
          <FormError ref={errorRef} message={formError} />
          <SelectField
            label="Document type"
            name="document_type"
            value={documentType}
            onChange={(event) => setDocumentType(event.target.value)}
            options={typeOptions.map((code) => ({ value: code, label: documentTypeLabel(code) }))}
            disabled={!!progress}
          />
          {sides.length > 0 ? (
            <SelectField
              key={documentType}
              label="Side"
              name="side"
              placeholderOption="Choose a side"
              options={sides.map((s) => ({ value: s, label: sideLabel(s) }))}
              error={fieldErrors.side}
              disabled={!!progress}
            />
          ) : null}
          <FileField inputRef={fileRef} error={fieldErrors.file} disabled={!!progress} />
          {progress ? (
            <div className="space-y-1">
              <label htmlFor={`${headingId}-progress`} className="block text-sm font-semibold text-ink-900">
                Upload progress: {percent}%
              </label>
              <progress id={`${headingId}-progress`} max={100} value={percent} className="h-3 w-full accent-brand-700">
                {percent}%
              </progress>
            </div>
          ) : null}
          <div className="flex flex-wrap gap-3">
            <SubmitButton busy={!!progress} busyLabel="Uploading…">
              Upload
            </SubmitButton>
            {progress ? (
              <Button variant="outline" onClick={() => abortRef.current?.abort()}>
                Cancel upload
              </Button>
            ) : null}
          </div>
        </form>
      ) : null}

      {pendingDelete ? (
        <Dialog title="Delete this document?" description={`${documentTypeLabel(pendingDelete.document_type)}${sideLabel(pendingDelete.side) ? ` (${sideLabel(pendingDelete.side).toLowerCase()})` : ""} will be removed from this verification.`} onClose={() => setPendingDelete(null)}>
          <div className="flex flex-wrap gap-3">
            <Button onClick={confirmDelete}>Delete document</Button>
            <Button variant="outline" onClick={() => setPendingDelete(null)}>
              Keep it
            </Button>
          </div>
        </Dialog>
      ) : null}
    </section>
  );
}

function requirementReady(req: DocumentRequirement, documents: VerificationDocument[]): boolean {
  const types = req.document_types ?? [req.document_type];
  const clean = documents.filter((d) => types.includes(d.document_type) && d.status === "CLEAN");
  if (req.sides.length === 0) return clean.length > 0;
  return req.sides.every((side) => clean.some((d) => d.side === side));
}

function humaniseReason(reason: string): string {
  const map: Record<string, string> = {
    MALWARE_DETECTED: "the file failed the malware check",
    CONTENT_INVALID: "the file could not be read as a valid image or PDF",
    TYPE_MISMATCH: "the file's contents do not match its type",
  };
  return map[reason] ?? reason.toLowerCase().replace(/_/g, " ");
}

export function fileProblemMessage(problem: string): string {
  switch (problem) {
    case "EMPTY_FILE":
      return "This file is empty. Choose a different file.";
    case "FILE_TOO_LARGE":
      return "This file is larger than 10 MB. Choose a smaller photo or a PDF.";
    case "UNSUPPORTED_FILE_TYPE":
      return "Choose a JPEG or PNG photo, or a PDF.";
    default:
      return "This file can't be used.";
  }
}

function FileField({ inputRef, error, disabled }: { inputRef: React.RefObject<HTMLInputElement | null>; error?: string; disabled?: boolean }) {
  const id = useId();
  const [chosen, setChosen] = useState<string | null>(null);
  return (
    <div className="space-y-1">
      <label htmlFor={id} className="block font-semibold text-ink-900">
        File
      </label>
      <p id={`${id}-hint`} className="text-sm text-ink-600">
        A clear photo (JPEG or PNG) or a PDF, up to 10 MB. Make sure all four corners are visible and the text is readable.
      </p>
      <input
        ref={inputRef}
        id={id}
        type="file"
        name="file"
        accept={ACCEPT_ATTRIBUTE}
        disabled={disabled}
        aria-invalid={error ? true : undefined}
        aria-describedby={[error ? `${id}-error` : null, `${id}-hint`].filter(Boolean).join(" ")}
        onChange={(event) => {
          const file = event.target.files?.[0];
          setChosen(file ? `${file.name} (${formatBytes(file.size)})` : null);
        }}
        className="block min-h-11 w-full rounded-xl border-2 border-line-strong bg-surface px-3 py-2 text-base text-ink-900 file:mr-3 file:rounded-full file:border-0 file:bg-brand-700 file:px-4 file:py-2 file:font-semibold file:text-white aria-[invalid=true]:border-danger-700"
      />
      {chosen ? <p className="text-sm text-ink-700">Selected: {chosen}</p> : null}
      {error ? (
        <p id={`${id}-error`} className="text-sm font-semibold text-danger-700">
          <span aria-hidden="true">Error: </span>
          {error}
        </p>
      ) : null}
    </div>
  );
}
