import { StagePlaceholder } from "@/components/stage-placeholder";

export default function CampaignNotFound() {
  return (
    <StagePlaceholder
      title="Campaign pages are coming soon"
      stage="Stage 7"
      description="There are no live campaigns on FundZim yet, so this link does not lead to a real campaign. If someone sent you this link asking for money, please do not send money through any other channel on the strength of it."
      planned={[
        "Reviewed campaigns with the owner's story, updates and verification status",
        "Raised amounts shown per currency (USD and ZiG), never combined",
        "A way to report a campaign you are concerned about",
      ]}
    />
  );
}
