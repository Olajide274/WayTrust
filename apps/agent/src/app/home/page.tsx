"use client";

import { useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import { motion } from "framer-motion";

const API = process.env.NEXT_PUBLIC_API_URL || "http://localhost:8080";

type Agent = {
  full_name?: string;
  phone_number?: string;
  park_id?: string;
  wallet_balance_kobo?: number;
  is_admin?: boolean;
};

function naira(kobo: number) {
  return (Number(kobo || 0) / 100).toLocaleString("en-NG", {
    style: "currency",
    currency: "NGN",
  });
}

export default function HomePage() {
  const router = useRouter();
  const [agent, setAgent] = useState<Agent | null>(null);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    const token = localStorage.getItem("nurtw_token");
    if (!token) {
      router.replace("/");
      return;
    }

    (async () => {
      try {
        const res = await fetch(`${API}/api/v1/agent/profile`, {
          headers: { Authorization: `Bearer ${token}` },
        });
        if (res.status === 401) {
          localStorage.removeItem("nurtw_token");
          router.replace("/");
          return;
        }
        const data = await res.json();
        if (!res.ok) throw new Error(data.error || "Could not load profile");
        setAgent(data);
      } catch (e) {
        setError(e instanceof Error ? e.message : "Network error");
      } finally {
        setLoading(false);
      }
    })();
  }, [router]);

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
              NURTW Escrow
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

        <section className="rounded-2xl border border-dashed border-slate-200 bg-white p-6 text-sm text-slate-500">
          Book waybill and delivery screens come next.
        </section>
      </motion.div>
    </main>
  );
}