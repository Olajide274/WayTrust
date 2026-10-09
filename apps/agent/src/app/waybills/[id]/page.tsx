"use client";

import { useCallback, useEffect, useState } from "react";
import Link from "next/link";
import { useParams, useRouter } from "next/navigation";
import { motion } from "framer-motion";
import { PRODUCT_NAME } from "@/lib/branding";
import {
  ApiError,
  getWaybillStatus,
  getParks,
  hasAuthToken,
  reportReceiverRefusal,
  startWaybillTransit,
  type WaybillStatus,
} from "@/lib/api";
import { CancelWaybillButton } from "@/components/cancel-waybill-button";
import { formatDate, formatNaira, formatPark, formatStatus, userErrorMessage } from "@/lib/format";

export default function WaybillDetailPage() {
  const { id } = useParams<{ id: string }>();
  const router = useRouter();
  const [waybill, setWaybill] = useState<WaybillStatus | null>(null);
  const [parks, setParks] = useState<Awaited<ReturnType<typeof getParks>>>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [transitError, setTransitError] = useState("");
  const [startingTransit, setStartingTransit] = useState(false);
  const [refusalReason, setRefusalReason] = useState("");
  const [refusalError, setRefusalError] = useState("");
  const [reportingRefusal, setReportingRefusal] = useState(false);
  const [notFound, setNotFound] = useState(false);

  const loadWaybill = useCallback(async () => {
    setLoading(true);
    setError("");
    setNotFound(false);
    setWaybill(null);

    try {
      if (!hasAuthToken()) {
        router.replace("/");
        return;
      }

      const [details, loadedParks]: [WaybillStatus, Awaited<ReturnType<typeof getParks>>] = await Promise.all([
        getWaybillStatus(id),
        getParks(),
      ]);
      setWaybill(details);
      setParks(loadedParks);
    } catch (requestError) {
      console.error("Could not load waybill details", requestError);
      if (requestError instanceof ApiError && requestError.status === 401) {
        router.replace("/");
      } else if (
        requestError instanceof ApiError &&
        requestError.status === 404
      ) {
        setNotFound(true);
      } else {
        setError(userErrorMessage(requestError, "Could not load this waybill. Please try again."));
      }
    } finally {
      setLoading(false);
    }
  }, [id, router]);

  useEffect(() => {
    void Promise.resolve().then(() => loadWaybill());
  }, [loadWaybill]);

  const backLink = (
    <Link href="/home" className="text-sm font-medium text-emerald-700 hover:underline">
      Back to home
    </Link>
  );

  async function onStartTransit() {
    setTransitError("");
    setStartingTransit(true);
    try {
      await startWaybillTransit(id);
      await loadWaybill();
    } catch (requestError) {
      console.error("Could not start waybill transit", requestError);
      setTransitError(userErrorMessage(requestError, "Could not start transit. Please try again."));
    } finally {
      setStartingTransit(false);
    }
  }

  async function onReportReceiverRefusal(e: React.FormEvent<HTMLFormElement>) {
    e.preventDefault();
    setRefusalError("");
    setReportingRefusal(true);
    try {
      await reportReceiverRefusal(id, refusalReason);
      setRefusalReason("");
      await loadWaybill();
    } catch (requestError) {
      console.error("Could not report receiver refusal", requestError);
      setRefusalError(userErrorMessage(requestError, "Could not report the receiver refusal. Please try again."));
    } finally {
      setReportingRefusal(false);
    }
  }

  const status = formatStatus(waybill?.current_state || "CREATED");

  return (
    <main className="min-h-screen bg-slate-50 px-4 py-8">
      <motion.div
        initial={{ opacity: 0, y: 8 }}
        animate={{ opacity: 1, y: 0 }}
        className="mx-auto max-w-lg"
      >
        {backLink}
        <p className="mt-4 text-xs font-medium uppercase tracking-wide text-emerald-700">
          {PRODUCT_NAME}
        </p>
        <h1 className="mt-1 text-2xl font-semibold text-slate-900">
          Waybill details
        </h1>

        {loading ? (
          <section aria-busy="true" aria-label="Loading waybill details" className="mt-6 space-y-6 rounded-2xl border border-slate-200 bg-white p-6 shadow-sm">
            <div className="flex justify-between gap-4">
              <div className="h-5 w-40 animate-pulse rounded bg-slate-200" />
              <div className="h-7 w-24 animate-pulse rounded-full bg-slate-100" />
            </div>
            <div className="h-4 w-20 animate-pulse rounded bg-slate-100" />
            <div className="h-6 w-3/4 animate-pulse rounded bg-slate-200" />
            <div className="h-4 w-16 animate-pulse rounded bg-slate-100" />
            <div className="h-7 w-32 animate-pulse rounded bg-slate-200" />
          </section>
        ) : notFound ? (
          <section className="mt-6 rounded-2xl border border-slate-200 bg-white p-6 shadow-sm">
            <h2 className="font-semibold text-slate-900">Waybill not found</h2>
            <p className="mt-2 text-sm text-slate-600">
              This waybill is unavailable or does not belong to your account.
            </p>
          </section>
        ) : error ? (
          <section className="mt-6 rounded-2xl border border-slate-200 bg-white p-6 shadow-sm">
            <p className="rounded-lg border border-red-100 bg-red-50 px-3 py-2 text-sm text-red-700">
              {error}
            </p>
            <button
              type="button"
              onClick={() => void loadWaybill()}
              className="mt-4 rounded-lg border border-slate-200 px-4 py-2.5 text-sm font-medium text-slate-700 hover:bg-slate-50"
            >
              Retry
            </button>
          </section>
        ) : waybill ? (
          <section className="mt-6 rounded-2xl border border-slate-200 bg-white p-6 shadow-sm">
            <div className="flex flex-wrap items-start justify-between gap-3">
              <p className="break-all text-sm font-semibold text-slate-900">
                {waybill.waybill_id}
              </p>
              <span
                className={`rounded-full border px-2.5 py-1 text-xs font-medium ${status.className}`}
              >
                {status.label}
              </span>
            </div>

            <div className="mt-6 space-y-5">
              <div>
                <p className="text-xs font-medium uppercase tracking-wide text-slate-500">
                  Route
                </p>
                <p className="mt-1 text-base font-medium text-slate-900">
                  {formatPark(waybill.origin_park_id, parks)}{" "}
                  <span aria-hidden="true" className="text-emerald-700">
                    →
                  </span>{" "}
                  {formatPark(waybill.destination_park_id, parks)}
                </p>
              </div>
              <div>
                <p className="text-xs font-medium uppercase tracking-wide text-slate-500">
                  Freight
                </p>
                <p className="mt-1 text-xl font-semibold text-slate-900">
                  {formatNaira(waybill.total_freight_kobo)}
                </p>
              </div>
              <div>
                <p className="text-xs font-medium uppercase tracking-wide text-slate-500">
                  Created
                </p>
                <p className="mt-1 text-sm text-slate-800">
                  {formatDate(waybill.created_at)}
                </p>
              </div>
            </div>
            {waybill.current_state === "ESCROW_LOCKED" ? (
              <div className="mt-6 border-t border-slate-100 pt-5">
                <p className="mb-3 text-sm text-slate-600">
                  Start the 24-hour unclaimed timer when the parcel leaves the origin park.
                </p>
                {transitError ? (
                  <p className="mb-3 rounded-lg border border-red-100 bg-red-50 px-3 py-2 text-sm text-red-700">
                    {transitError}
                  </p>
                ) : null}
                <button
                  type="button"
                  onClick={() => void onStartTransit()}
                  disabled={startingTransit}
                  className="w-full rounded-lg bg-emerald-700 px-4 py-3 text-sm font-medium text-white hover:bg-emerald-800 disabled:opacity-60"
                >
                  {startingTransit ? "Starting transit..." : "Start transit"}
                </button>
                <div className="mt-3">
                  <CancelWaybillButton
                    waybillId={waybill.waybill_id}
                    onSuccess={loadWaybill}
                  />
                </div>
              </div>
            ) : null}
            {waybill.current_state === "IN_TRANSIT" ? (
              <form
                onSubmit={onReportReceiverRefusal}
                className="mt-6 border-t border-slate-100 pt-5"
              >
                <h2 className="font-semibold text-slate-900">
                  Receiver refused the parcel?
                </h2>
                <p className="mt-1 text-sm text-slate-600">
                  Reporting this places the waybill on dispute hold. Funds will
                  remain held until a resolution process is completed.
                </p>
                <label
                  htmlFor="refusal-reason"
                  className="mb-1.5 mt-4 block text-sm font-medium text-slate-700"
                >
                  Reason for refusal
                </label>
                <textarea
                  id="refusal-reason"
                  value={refusalReason}
                  onChange={(event) => setRefusalReason(event.target.value)}
                  maxLength={500}
                  rows={3}
                  required
                  className="w-full rounded-lg border border-slate-300 px-3 py-2.5 text-slate-900 focus:border-emerald-600 focus:outline-none focus:ring-2 focus:ring-emerald-600/30"
                />
                {refusalError ? (
                  <p className="mt-3 rounded-lg border border-red-100 bg-red-50 px-3 py-2 text-sm text-red-700">
                    {refusalError}
                  </p>
                ) : null}
                <button
                  type="submit"
                  disabled={reportingRefusal}
                  className="mt-3 w-full rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm font-medium text-red-800 hover:bg-red-100 disabled:opacity-60"
                >
                  {reportingRefusal ? "Reporting refusal..." : "Report receiver refusal"}
                </button>
              </form>
            ) : null}
            {waybill.receiver_refusal_reason ? (
              <div className="mt-6 rounded-lg border border-orange-200 bg-orange-50 p-4">
                <h2 className="font-semibold text-orange-900">
                  Receiver refusal reported
                </h2>
                <p className="mt-1 text-sm text-orange-900">
                  {waybill.receiver_refusal_reason}
                </p>
                <p className="mt-2 text-sm text-orange-800">
                  Funds remain on hold pending dispute resolution.
                </p>
              </div>
            ) : null}
          </section>
        ) : null}
      </motion.div>
    </main>
  );
}
