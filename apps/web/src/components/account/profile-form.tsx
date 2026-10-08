"use client";

import { useRouter } from "next/navigation";
import { useState, type FormEvent } from "react";

import { useFormFeedback } from "@/components/auth/use-form-feedback";
import { FormError, FormStatus } from "@/components/forms/form-error";
import { SubmitButton } from "@/components/forms/submit-button";
import { TextField } from "@/components/forms/text-field";
import { authApi, type AuthApi } from "@/lib/auth/api";

const DISPLAY_NAME_MAX = 80;

export function ProfileForm({ displayName, api = authApi }: { displayName: string; api?: AuthApi }) {
  const router = useRouter();
  const [busy, setBusy] = useState(false);
  const [saved, setSaved] = useState(false);
  const { formError, fieldErrors, errorRef, formRef, clear, fail, failWith } = useFormFeedback();

  async function onSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setSaved(false);
    const value = String(new FormData(event.currentTarget).get("display_name") ?? "").trim();
    if (!value) return fail("Please correct the errors below.", { display_name: "Enter the name you want to be shown." });
    if (value.length > DISPLAY_NAME_MAX) return fail("Please correct the errors below.", { display_name: `Use ${DISPLAY_NAME_MAX} characters or fewer.` });
    clear();
    setBusy(true);
    try {
      await api.updateProfile(value);
      setSaved(true);
      router.refresh();
    } catch (error) {
      failWith(error, "We couldn't save your display name. Please try again.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <form ref={formRef} noValidate onSubmit={onSubmit} className="space-y-5">
      <FormError ref={errorRef} message={formError} />
      {saved ? <FormStatus title="Display name saved" /> : null}
      <TextField
        label="Display name"
        name="display_name"
        autoComplete="nickname"
        defaultValue={displayName}
        maxLength={DISPLAY_NAME_MAX}
        required
        error={fieldErrors.display_name}
      />
      <SubmitButton busy={busy} busyLabel="Saving…">
        Save display name
      </SubmitButton>
    </form>
  );
}
