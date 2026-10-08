"use client";

import { useCallback, useEffect, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { motion } from "framer-motion";
import { PRODUCT_NAME } from "@/lib/branding";
import {
  ApiError,
  getAgentProfile,
  getAgentWaybills,
  getParks,
  PROFILE_REFRESH_EVENT,
  type AgentWaybill,
  type AgentProfile,
  type Park,
} from "@/lib/api";
import { formatNaira, formatPark, formatStatus, userErrorMessage } from "@/lib/format";

export default function HomePage() {
  const router = useRouter();
  const [agent, setAgent] = useState<AgentProfile | null>(null);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);
  const [waybills, setWaybills] = useState<AgentWaybill[]>([]);
  const [waybillsError, setWaybillsError] = useState("");
  const [waybillsLoading, setWaybillsLoading] = useState(true);
  const [parks, setParks] = useState<Park[]>([]);
  const [parksLoading, setParksLoading] = useState(true);

  useEffect(() => {
    const token = localStorage.getItem("nurtw_token");
    if (!token) {
      router.replace("/");
      return;
    }

    let active = true;
    async function loadProfile() {
      try {
        const profile = await getAgentProfile();
        if (active) {
          setAgent(profile);
          setError("");
        }
      } catch (e) {
        if (!active) return;
        if (e instanceof ApiError && e.status === 401) {
          router.replace("/");
        } else {
          console.error("Could not load agent profile", e);
          setError(userErrorMessage(e, "Could not load your profile. Please try again."));
        }
      } finally {
        if (active) setLoading(false);
      }
    }

    void loadProfile();
    window.addEventListener(PROFILE_REFRESH_EVENT, loadProfile);
    return () => {
      active = false;
      window.removeEventListener(PROFILE_REFRESH_EVENT, loadProfile);
    };
  }, [router]);

  const loadWaybills = useCallback(async () => {
    setWaybillsLoading(true);
    setWaybillsError("");
    try {
      setWaybills(await getAgentWaybills());
    } catch (requestError) {
      console.error("Could not load agent waybills", requestError);
      if (requestError instanceof ApiError && requestError.status === 401) {
        router.replace("/");
        return;
      }
      setWaybillsError(userErrorMessage(requestError, "Could not load your waybills. Please try again."));
    } finally {
      setWaybillsLoading(false);
    }
  }, [router]);

  useEffect(() => {
    if (!localStorage.getItem("nurtw_token")) {
      router.replace("/");
      return;
    }

    let active = true;
    void Promise.resolve().then(() => {
      if (active) return loadWaybills();
    });
    getParks()
      .then((parks) => {
        if (!active) return;
        setParks(parks);
      })
      .catch((requestError: unknown) => {
        console.error("Could not load parks for home", requestError);
        if (
          active &&
          requestError instanceof ApiError &&
          requestError.status === 401
        ) {
          router.replace("/");
        }
      })
      .finally(() => {
        if (active) setParksLoading(false);
      });

    return () => {
      active = false;
    };
  }, [loadWaybills, router]);

  function logout() {
    localStorage.removeItem("nurtw_token");
    router.replace("/");
  }

  if (loading) {
    return (
      <main className="min-h-screen bg-slate-50 px-4 py-8">
        <div
          className="mx-auto max-w-lg space-y-4"
          aria-busy="true"
          aria-label="Loading home"
        >
          <div className="flex items-start justify-between gap-4">
            <div className="space-y-2">
              <div className="h-3 w-24 animate-pulse rounded bg-slate-200" />
              <div className="h-8 w-32 animate-pulse rounded bg-slate-200" />
            </div>
            <div className="h-9 w-20 animate-pulse rounded-lg bg-slate-200" />
          </div>
          <section className="space-y-4 rounded-2xl border border-slate-200 bg-white p-6 shadow-sm">
            <div className="h-4 w-28 animate-pulse rounded bg-slate-200" />
            <div className="h-9 w-40 animate-pulse rounded bg-slate-200" />
            <div className="mt-4 h-4 w-48 animate-pulse rounded bg-slate-200" />
            <div className="h-4 w-56 animate-pulse rounded bg-slate-200" />
          </section>
          <section className="space-y-4 rounded-2xl border border-slate-200 bg-white p-6 shadow-sm">
            <div className="h-6 w-28 animate-pulse rounded bg-slate-200" />
            <div className="h-4 w-64 animate-pulse rounded bg-slate-100" />
            <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
              <div className="h-12 animate-pulse rounded-lg bg-slate-100" />
              <div className="h-12 animate-pulse rounded-lg bg-slate-100" />
            </div>
          </section>
          <section className="space-y-4 rounded-2xl border border-slate-200 bg-white p-6 shadow-sm">
            <div className="h-6 w-28 animate-pulse rounded bg-slate-200" />
            {[0, 1].map((item) => (
              <div key={item} className="space-y-2 border-b border-slate-100 py-4">
                <div className="h-4 w-3/4 animate-pulse rounded bg-slate-200" />
                <div className="h-4 w-1/3 animate-pulse rounded bg-slate-100" />
              </div>
            ))}
          </section>
        </div>
      </main>
    );
  }

  return (
    <main className="min-h-screen bg-slate-50 px-4 py-8">
      <motion.div
        initial={{ opacity: 0, y: 10 }}
        animate={{ opacity: 1, y: 0 }}
        transition={{ duration: 0.3 }}
        className="mx-auto max-w-lg space-y-4"
      >
        <div className="flex items-start justify-between gap-4">
          <div>
            <p className="text-xs font-medium uppercase tracking-wide text-emerald-700">
              {PRODUCT_NAME}
            </p>
            <h1 className="text-2xl font-semibold text-slate-900">Home</h1>
          </div>
          <button
            type="button"
            onClick={logout}
            className="rounded-lg border border-slate-200 bg-white px-3 py-2 text-sm font-medium text-slate-700 hover:bg-slate-50"
          >
            Log out
          </button>
        </div>

        {error ? (
          <p className="rounded-lg border border-red-100 bg-red-50 px-3 py-2 text-sm text-red-600">
            {error}
          </p>
        ) : null}

        <section className="rounded-2xl border border-slate-200 bg-white p-6 shadow-sm">
          <p className="text-sm text-slate-500">Wallet balance</p>
          <p className="mt-1 text-3xl font-semibold tracking-tight text-slate-900">
            {agent ? formatNaira(agent.wallet_balance_kobo || 0) : "—"}
          </p>
          <p className="mt-4 text-sm text-slate-700">
            {agent?.full_name || "Agent"}
          </p>
          <p className="text-sm text-slate-500">
            {parksLoading ? (
              <span className="inline-block h-4 w-32 animate-pulse rounded bg-slate-200 align-middle" />
            ) : (
              formatPark(agent?.park_id, parks)
            )}
            {agent?.phone_number ? ` · ${agent.phone_number}` : ""}
          </p>
        </section>

        <section className="rounded-2xl border border-slate-200 bg-white p-6 shadow-sm">
          <h2 className="text-lg font-semibold text-slate-900">Waybills</h2>
          <p className="mt-1 text-sm text-slate-500">
            Book a waybill or verify a delivery PIN.
          </p>
          <div className="mt-5 grid grid-cols-1 gap-3 sm:grid-cols-2">
            <Link
              href="/waybills/new"
              className="inline-flex w-full items-center justify-center rounded-lg bg-emerald-700 px-4 py-3 text-sm font-medium text-white transition-colors hover:bg-emerald-800"
            >
              Book waybill
            </Link>
            <Link
              href="/waybills/deliver"
              className="inline-flex w-full items-center justify-center rounded-lg border border-emerald-700 px-4 py-3 text-sm font-medium text-emerald-800 transition-colors hover:bg-emerald-50"
            >
              Verify PIN
            </Link>
          </div>
        </section>

        <section className="rounded-2xl border border-slate-200 bg-white p-6 shadow-sm">
          <h2 className="text-lg font-semibold text-slate-900">My waybills</h2>
          {waybillsLoading ? (
            <div className="mt-4 space-y-4" aria-busy="true" aria-label="Loading waybills">
              {[0, 1, 2].map((item) => (
                <div key={item} className="space-y-2 border-b border-slate-100 py-4 last:border-0">
                  <div className="h-4 w-3/4 animate-pulse rounded bg-slate-200" />
                  <div className="h-4 w-1/3 animate-pulse rounded bg-slate-100" />
                  <div className="h-4 w-2/3 animate-pulse rounded bg-slate-100" />
                </div>
              ))}
            </div>
          ) : waybillsError ? (
            <div className="mt-4">
              <p className="rounded-lg border border-red-100 bg-red-50 px-3 py-2 text-sm text-red-600">
                {waybillsError}
              </p>
              <button
                type="button"
                onClick={() => void loadWaybills()}
                className="mt-3 rounded-lg border border-slate-200 px-3 py-2 text-sm font-medium text-slate-700 hover:bg-slate-50"
              >
                Retry
              </button>
            </div>
          ) : waybills.length === 0 ? (
            <p className="mt-4 text-sm text-slate-500">
              No waybills yet. Book a waybill to see it here.
            </p>
          ) : (
            <ul className="mt-4 divide-y divide-slate-100">
              {waybills.map((waybill) => {
                const status = formatStatus(waybill.current_state);
                return (
                  <li key={waybill.id} className="py-4 first:pt-0 last:pb-0">
                    <Link
                      href={`/waybills/${encodeURIComponent(waybill.id)}`}
                      aria-label={`View waybill ${waybill.id}`}
                      className="block rounded-lg focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-emerald-600"
                    >
                      <div className="flex items-start justify-between gap-3">
                        <p className="break-all text-sm font-semibold text-slate-900">
                          {waybill.id}
                        </p>
                        <span
                          className={`shrink-0 rounded-full border px-2.5 py-1 text-xs font-medium ${status.className}`}
                        >
                          {status.label}
                        </span>
                      </div>
                      <p className="mt-2 text-sm font-medium text-slate-800">
                        {formatNaira(waybill.total_freight_kobo)}
                      </p>
                      <p className="mt-1 text-sm text-slate-500">
                        {formatPark(waybill.origin_park_id, parks)} →{" "}
                        {formatPark(waybill.destination_park_id, parks)}
                      </p>
                      <p className="mt-2 text-xs font-medium text-emerald-700">
                        View details
                      </p>
                    </Link>
                  </li>
                );
              })}
            </ul>
          )}
        </section>
      </motion.div>
    </main>
  );
}