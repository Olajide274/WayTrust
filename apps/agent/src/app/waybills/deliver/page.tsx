"use client";

import { useState } from "react";
import { useRouter } from "next/navigation";
import Link from "next/link";
import { motion } from "framer-motion";
import { PRODUCT_NAME } from "@/lib/branding";
import { ApiError, AUTH_TOKEN_KEY, verifyWaybillPin } from "@/lib/api";
import { formatPINVerification, userErrorMessage } from "@/lib/format";

export default function DeliverPage() {
  const router = useRouter();
  const [waybillId, setWaybillId] = useState("");
  const [pin, setPin] = useState("");
  const [error, setError] = useState("");
  const [success, setSuccess] = useState("");
  const [loading, setLoading] = useState(false);

  async function onSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError("");
    setSuccess("");
    setLoading(true);
    try {
      const token = localStorage.getItem(AUTH_TOKEN_KEY);
      if (!token) {
        router.replace("/");
        return;
      }
      const data = await verifyWaybillPin(waybillId.trim(), pin.trim());
      setSuccess(formatPINVerification(data.state));
    } catch (err) {
      console.error("Could not verify delivery PIN", err);
      if (err instanceof ApiError && err.status === 401) {
        router.replace("/");
        return;
      }
      setError(userErrorMessage(err, "Could not verify the PIN. Check it and try again."));
    } finally {
      setLoading(false);
    }
  }

  return (
    <main className="min-h-screen bg-slate-50 px-4 py-8">
      <motion.div
        initial={{ opacity: 0, y: 8 }}
        animate={{ opacity: 1, y: 0 }}
        className="mx-auto max-w-md"
      >
        <Link href="/home" className="text-sm text-emerald-700 hover:underline">
          Back to home
        </Link>
        <p className="mt-4 text-xs font-medium uppercase tracking-wide text-emerald-700">
          {PRODUCT_NAME}
        </p>
        <h1 className="text-2xl font-semibold text-slate-900">Verify delivery PIN</h1>
        <p className="mt-1 text-sm text-slate-500">
          Receiver gives the 4-digit PIN to release the parcel.
        </p>

        <form
          onSubmit={onSubmit}
          className="mt-6 space-y-4 rounded-2xl border border-slate-200 bg-white p-6 shadow-sm"
        >
          <div>
            <label className="mb-1.5 block text-sm font-medium text-slate-700">
              Waybill ID
            </label>
            <input
              value={waybillId}
              onChange={(e) => setWaybillId(e.target.value)}
              placeholder="WB-515bf7ca"
              autoComplete="off"
              className="w-full rounded-lg border border-slate-300 px-3 py-2.5 text-slate-900 focus:border-emerald-600 focus:outline-none focus:ring-2 focus:ring-emerald-600/30"
              required
            />
          </div>
          <div>
            <label className="mb-1.5 block text-sm font-medium text-slate-700">
              4-digit PIN
            </label>
            <input
              type="password"
              inputMode="numeric"
              maxLength={4}
              value={pin}
              onChange={(e) => setPin(e.target.value)}
              placeholder="9826"
              className="w-full rounded-lg border border-slate-300 px-3 py-2.5 text-slate-900 focus:border-emerald-600 focus:outline-none focus:ring-2 focus:ring-emerald-600/30"
              required
            />
          </div>

          {error ? (
            <p className="rounded-lg border border-red-100 bg-red-50 px-3 py-2 text-sm text-red-600">
              {error}
            </p>
          ) : null}
          {success ? (
            <p className="rounded-lg border border-emerald-100 bg-emerald-50 px-3 py-2 text-sm text-emerald-800">
              {success}
            </p>
          ) : null}

          <button
            type="submit"
            disabled={loading}
            className="w-full rounded-lg bg-emerald-700 py-2.5 font-medium text-white hover:bg-emerald-800 disabled:opacity-60"
          >
            {loading ? "Verifying..." : "Verify PIN and mark delivered"}
          </button>
        </form>
      </motion.div>
    </main>
  );
}
