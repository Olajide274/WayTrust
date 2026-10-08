"use client";

import { useCallback, useEffect, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import {
  ApiError,
  getSettlementSummary,
  type SettlementSummary,
} from "@/lib/api";
import { userErrorMessage } from "@/lib/format";

const groups = [
  { key: "paid", label: "Paid" },
  { key: "pending", label: "Pending" },
  { key: "held", label: "Held" },
  { key: "refunded", label: "Refunded" },
] as const;

const stakeholders = [
  { key: "driver_payout_kobo", label: "Driver" },
  { key: "agent_commission_kobo", label: "Agent commission" },
  { key: "union_dues_kobo", label: "Union dues" },
  { key: "platform_treasury_kobo", label: "Platform treasury" },
] as const;

export default function SettlementSummaryPage() {
  const router = useRouter();
  const [summary, setSummary] = useState<SettlementSummary | null>(null);
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");

  const loadSummary = useCallback(async (filters: { from?: string; to?: string } = {}) => {
    setLoading(true);
    setError("");
    try {
      setSummary(await getSettlementSummary(filters));
    } catch (requestError) {
      console.error("Could not load settlement summary", requestError);
      if (requestError instanceof ApiError && requestError.status === 401) {
        router.replace("/");
        return;
      }
      setError(
        requestError instanceof ApiError && requestError.status === 403
          ? "Admin access is required to view settlement totals."
          : userErrorMessage(requestError, "Could not load settlement totals."),
      );
      setSummary(null);
    } finally {
      setLoading(false);
    }
  }, [router]);

  useEffect(() => {
    if (!localStorage.getItem("nurtw_token")) {
      router.replace("/");
      return;
    }
    let active = true;
    void Promise.resolve().then(() => {
      if (active) void loadSummary();
    });
    return () => {
      active = false;
    };
  }, [loadSummary, router]);

  function applyDateFilter(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    void loadSummary({ from: from || undefined, to: to || undefined });
  }

  const hasSettlements = summary
    ? groups.some(({ key }) => summary[key].count > 0)
    : false;

  return (
    <main className="min-h-screen bg-slate-50 px-4 py-8">
      <div className="mx-auto max-w-3xl space-y-5">
        <header>
          <Link
            href="/home"
            className="text-sm font-medium text-emerald-700 hover:text-emerald-800"
          >
            ← Home
          </Link>
          <p className="mt-5 text-xs font-medium uppercase tracking-wide text-emerald-700">
            Administration
          </p>
          <h1 className="mt-1 text-2xl font-semibold text-slate-900">
            Settlement summary
          </h1>
          <p className="mt-2 text-sm text-slate-600">
            All amounts are in kobo. Date filters use disbursement date for paid
            waybills and creation date for pending, held, and refunded waybills.
          </p>
        </header>

        <form
          onSubmit={applyDateFilter}
          className="grid grid-cols-1 gap-3 rounded-2xl border border-slate-200 bg-white p-5 shadow-sm sm:grid-cols-[1fr_1fr_auto]"
        >
          <label className="text-sm font-medium text-slate-700">
            From
            <input
              type="date"
              value={from}
              onChange={(event) => setFrom(event.target.value)}
              className="mt-1 block w-full rounded-lg border border-slate-300 px-3 py-2 text-slate-900"
            />
          </label>
          <label className="text-sm font-medium text-slate-700">
            To
            <input
              type="date"
              value={to}
              onChange={(event) => setTo(event.target.value)}
              className="mt-1 block w-full rounded-lg border border-slate-300 px-3 py-2 text-slate-900"
            />
          </label>
          <button
            type="submit"
            disabled={loading}
            className="self-end rounded-lg bg-emerald-700 px-4 py-2 text-sm font-medium text-white hover:bg-emerald-800 disabled:opacity-60"
          >
            Apply dates
          </button>
        </form>

        {error ? (
          <div
            role="alert"
            className="rounded-lg border border-red-100 bg-red-50 px-3 py-2 text-sm text-red-700"
          >
            <p>{error}</p>
            <button
              type="button"
              onClick={() =>
                void loadSummary({ from: from || undefined, to: to || undefined })
              }
              className="mt-2 font-medium underline"
            >
              Retry
            </button>
          </div>
        ) : null}

        {loading ? (
          <section
            aria-busy="true"
            aria-label="Loading settlement totals"
            className="space-y-3 rounded-2xl border border-slate-200 bg-white p-6 shadow-sm"
          >
            <div className="h-5 w-40 animate-pulse rounded bg-slate-200" />
            <div className="h-4 w-full animate-pulse rounded bg-slate-100" />
            <div className="h-4 w-3/4 animate-pulse rounded bg-slate-100" />
          </section>
        ) : summary ? (
          hasSettlements ? (
            <div className="space-y-4">
              {groups.map(({ key, label }) => {
                const totals = summary[key];
                return (
                  <section
                    key={key}
                    className="rounded-2xl border border-slate-200 bg-white p-5 shadow-sm"
                  >
                    <div className="flex items-baseline justify-between gap-3">
                      <h2 className="text-lg font-semibold text-slate-900">
                        {label}
                      </h2>
                      <p className="text-sm text-slate-500">
                        {totals.count.toLocaleString()} waybills
                      </p>
                    </div>
                    <dl className="mt-4 grid grid-cols-1 gap-3 sm:grid-cols-2">
                      {stakeholders.map(({ key: stakeholderKey, label: stakeholderLabel }) => (
                        <div
                          key={stakeholderKey}
                          className="rounded-lg bg-slate-50 p-3"
                        >
                          <dt className="text-sm text-slate-500">
                            {stakeholderLabel}
                          </dt>
                          <dd className="mt-1 font-semibold text-slate-900">
                            {totals[stakeholderKey].toLocaleString("en-NG")} kobo
                          </dd>
                        </div>
                      ))}
                    </dl>
                  </section>
                );
              })}
            </div>
          ) : (
            <p className="rounded-2xl border border-slate-200 bg-white p-6 text-sm text-slate-500 shadow-sm">
              No settlement activity for the selected dates.
            </p>
          )
        ) : null}
      </div>
    </main>
  );
}
