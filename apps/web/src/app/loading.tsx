import { Container } from "@/components/ui/container";
import { LoadingSpinner } from "@/components/ui/loading-spinner";

export default function Loading() {
  return (
    <Container className="py-24">
      <LoadingSpinner label="Loading page…" />
    </Container>
  );
}
