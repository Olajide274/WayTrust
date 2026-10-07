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
} from "@/lib/api";

function naira(kobo: number) {
  return (Number(kobo || 0) / 100).toLocaleString("en-NG", {
    style: "currency",
    currency: "NGN",
  });
}

function statusLabel(state: string) {
  switch (state) {
    case "ESCROW_LOCKED":
      return "Locked";
    case "IN_TRANSIT":
      return "In transit";
    case "DELIVERED":
      return "Delivered";
    case "DISBURSED":
      return "Paid out";
    case "DISPUTE_HOLD":
      return "Dispute";
    case "UNCLAIMED_HOLD":
      return "Unclaimed";
    case "CANCELLED":
      return "Cancelled";
    default:
      return state || "Unknown";
  }
}

function statusClass(state: string) {
  switch (state) {
    case "ESCROW_LOCKED":
      return "bg-amber-50 text-amber-800 border-amber-200";
    case "IN_TRANSIT":
      return "bg-sky-50 text-sky-800 border-sky-200";
    case "DELIVERED":
      return "bg-emerald-50 text-emerald-800 border-emerald-200";
    case "DISBURSED":
      return "bg-teal-50 text-teal-900 border-teal-200";
    case "DISPUTE_HOLD":
      return "bg-red-50 text-red-800 border-red-200";
    case "UNCLAIMED_HOLD":
      return "bg-orange-50 text-orange-800 border-orange-200";
    case "CANCELLED":
      return "bg-slate-100 text-slate-600 border-slate-200";
    default:
      return "bg-slate-50 text-slate-600 border-slate-200";
  }
}

export default function HomePage() {
  const router = useRouter();
  const [agent, setAgent] = useState<AgentProfile | null>(null);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);
  const [waybills, setWaybills] = useState<AgentWaybill[]>([]);
  const [waybillsError, setWaybillsError] = useState("");
  const [waybillsLoading, setWaybillsLoading] = useState(true);
  const [parkNames, setParkNames] = useState<Record<string, string>>({});

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
          setError(e instanceof Error ? e.message : "Network error");
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
      if (requestError instanceof ApiError && requestError.status === 401) {
        router.replace("/");
        return;
      }
      setWaybillsError(
        requestError instanceof Error
          ? requestError.message
          : "Could not load your waybills.",
      );
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
        setParkNames(
          Object.fromEntries(parks.map((park) => [park.id, park.name])),
        );
      })
      .catch((requestError: unknown) => {
        if (
          active &&
          requestError instanceof ApiError &&
          requestError.status === 401
        ) {
          router.replace("/");
        }
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
      <main className="min-h-screen bg-slate-50 flex items-center justify-center">
        <p className="text-sm text-slate-500">Checking your session…</p>
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
            {agent ? naira(agent.wallet_balance_kobo || 0) : "—"}
          </p>
          <p className="mt-4 text-sm text-slate-700">
            {agent?.full_name || "Agent"}
          </p>
          <p className="text-sm text-slate-500">
            {agent?.park_id || "—"}
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
            <p className="mt-4 text-sm text-slate-500">
              Loading your waybills…
            </p>
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
              {waybills.map((waybill) => (
                <li key={waybill.id} className="py-4 first:pt-0 last:pb-0">
                  <div className="flex items-start justify-between gap-3">
                    <p className="break-all text-sm font-semibold text-slate-900">
                      {waybill.id}
                    </p>
                    <span
                      className={`shrink-0 rounded-full border px-2.5 py-1 text-xs font-medium ${statusClass(waybill.current_state)}`}
                    >
                      {statusLabel(waybill.current_state)}
                    </span>
                  </div>
                  <p className="mt-2 text-sm font-medium text-slate-800">
                    {naira(waybill.total_freight_kobo)}
                  </p>
                  <p className="mt-1 text-sm text-slate-500">
                    {parkNames[waybill.origin_park_id] ||
                      waybill.origin_park_id}{" "}
                    →{" "}
                    {parkNames[waybill.destination_park_id] ||
                      waybill.destination_park_id}
                  </p>
                </li>
              ))}
            </ul>
          )}
        </section>
      </motion.div>
    </main>
  );
}