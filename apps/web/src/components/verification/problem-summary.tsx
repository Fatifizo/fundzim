"use client";

import type { SubmissionProblem } from "@/lib/verification/errors";

/**
 * Error summary for SUBMISSION_INCOMPLETE: each problem names the field or document and links to it when
 * an element with id `field-<name>` exists on the page (fields and document sections set those ids).
 */
export function ProblemSummary({ problems }: { problems: SubmissionProblem[] }) {
  if (problems.length === 0) return null;
  return (
    <ul className="mt-2 list-disc space-y-1 pl-6 font-normal">
      {problems.map((problem, index) => (
        <li key={`${problem.field}-${index}`}>
          <a
            href={`#field-${problem.field.split(".")[0]}`}
            className="font-semibold underline"
            onClick={(event) => {
              const target = document.getElementById(`field-${problem.field.split(".")[0]}`);
              if (!target) return;
              event.preventDefault();
              target.focus();
              target.scrollIntoView({ block: "center" });
            }}
          >
            {problem.label}
          </a>
          : {problem.message}
        </li>
      ))}
    </ul>
  );
}
