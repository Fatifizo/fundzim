"use client";

import { useCallback, useState } from "react";

import { StepUpCancelledError } from "@/components/auth/step-up";
import { useFormFeedback } from "@/components/auth/use-form-feedback";
import { isAuthRequired } from "@/lib/auth/errors";
import { loginUrl } from "@/lib/auth/safe-redirect";
import { browserNavigation } from "@/lib/browser-navigation";
import { describeVerificationError, type SubmissionProblem } from "@/lib/verification/errors";

/**
 * Form feedback for verification screens: the Stage 4 focus-managing form feedback plus the Stage 5 error
 * mapping (SUBMISSION_INCOMPLETE problem list, upload codes, reviewer codes). A cancelled step-up is not an
 * error; an ended session redirects to sign-in.
 */
export function useVerificationFeedback() {
  const feedback = useFormFeedback();
  const [problems, setProblems] = useState<SubmissionProblem[]>([]);
  const { fail, clear: baseClear } = feedback;

  const clear = useCallback(() => {
    baseClear();
    setProblems([]);
  }, [baseClear]);

  const failWith = useCallback(
    (error: unknown, fallback?: string, fieldMap: Record<string, string> = {}) => {
      if (error instanceof StepUpCancelledError) return null;
      if (isAuthRequired(error)) {
        browserNavigation.assign(loginUrl(browserNavigation.currentPath()));
        return null;
      }
      const described = describeVerificationError(error, fallback);
      const fields: Record<string, string> = {};
      for (const [apiField, text] of Object.entries(described.fieldErrors)) fields[fieldMap[apiField] ?? apiField] = text;
      setProblems(described.problems);
      fail(described.message, fields, described.retryAfterSeconds);
      return described;
    },
    [fail],
  );

  return { ...feedback, problems, clear, failWith };
}
