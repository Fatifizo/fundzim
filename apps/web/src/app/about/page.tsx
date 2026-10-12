import type { Metadata } from "next";
import Link from "next/link";

import { ContentPage } from "@/components/content-page";
import { pageMetadata } from "@/lib/seo/metadata";

export function generateMetadata(): Metadata {
  return pageMetadata({ title: "About", description: "About FundZim, a Zimbabwe-first donation crowdfunding platform under development.", path: "/about" });
}

export default function AboutPage() {
  return (
    <ContentPage title="About FundZim" intro="Fund anyone in Zimbabwe, from anywhere.">
      <p>
        FundZim is a Zimbabwe-first donation crowdfunding platform. It is being built so that individuals and verified
        organisations in Zimbabwe can raise money for causes such as medical bills, school fees, funerals and community
        projects, and so that supporters at home and in the diaspora can give with payment methods that suit them.
      </p>

      <h2>Where we are today</h2>
      <p>
        FundZim is <strong>under development</strong>. People can create accounts, verify who they are and prepare
        campaigns; reviewers check every campaign before it is published; and published campaigns can be read and shared.
        <strong> Donations and payments are not available yet</strong>, and nothing here should be read as an offer to
        donate money.
      </p>

      <h2>What we are building</h2>
      <ul>
        <li>Mobile-first campaign pages that work well on slower connections and share easily on WhatsApp.</li>
        <li>Support for local payment methods and international cards through licensed payment service providers.</li>
        <li>USD and ZiG handled separately, with no automatic currency conversion.</li>
        <li>Identity verification, campaign review, reporting and payout checks to protect donors and beneficiaries.</li>
      </ul>

      <h2>What we are not</h2>
      <p>
        FundZim is donation-only. It does not offer investments, loans, interest, profit-sharing or rewards, and it is not a
        bank or a wallet.
      </p>
      <p>
        See <Link href="/how-it-works">how it works</Link> and <Link href="/safety">safety on FundZim</Link>.
      </p>
    </ContentPage>
  );
}
