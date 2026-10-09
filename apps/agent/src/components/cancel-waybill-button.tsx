"use client";

import { useId, useState } from "react";
import { cancelWaybill, PROFILE_REFRESH_EVENT } from "@/lib/api";
import { userErrorMessage } from "@/lib/format";

type CancelWaybillButtonProps = {
  waybillId: string;
  onSuccess?: () => void | Promise<void>;
};

export function CancelWaybillButton({
  waybillId,
  onSuccess,
}: CancelWaybillButtonProps) {
  const dialogTitleId = useId();
  const [confirming, setConfirming] = useState(false);
  const [cancelling, setCancelling] = useState(false);
  const [idempotencyKey, setIdempotencyKey] = useState("");
  const [error, setError] = useState("");

  async function confirmCancellation() {
    setError("");
    setCancelling(true);

    try {
      await cancelWaybill({
        waybill_id: waybillId,
        idempotency_key: idempotencyKey,
      });
      setConfirming(false);
      window.dispatchEvent(new Event(PROFILE_REFRESH_EVENT));
      await onSuccess?.();
    } catch (requestError) {
      console.error("Could not cancel waybill", requestError);
      setError(
        userErrorMessage(
          requestError,
          "Could not cancel this waybill. Please try again.",
        ),
      );
    } finally {
      setCancelling(false);
    }
  }

  function openConfirmation() {
    setError("");
    setIdempotencyKey(crypto.randomUUID());
    setConfirming(true);
  }

  function closeConfirmation() {
    if (cancelling) return;
    setConfirming(false);
    setIdempotencyKey("");
    setError("");
  }

  return (
    <>
      <button
        type="button"
        onClick={openConfirmation}
        disabled={cancelling}
        className="w-full rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm font-medium text-red-800 hover:bg-red-100 disabled:cursor-not-allowed disabled:opacity-60"
      >
        Cancel waybill
      </button>
      {confirming ? (
        <div
          className="fixed inset-0 z-50 flex items-end justify-center bg-slate-900/40 p-4 sm:items-center"
          role="presentation"
        >
          <section
            role="dialog"
            aria-modal="true"
            aria-labelledby={dialogTitleId}
            className="w-full max-w-sm rounded-2xl bg-white p-5 shadow-xl"
          >
            <h2
              id={dialogTitleId}
              className="text-lg font-semibold text-slate-900"
            >
              Cancel waybill?
            </h2>
            <p className="mt-2 text-sm text-slate-600">
              Cancel this waybill and return the freight to your wallet?
            </p>
            {error ? (
              <p
                role="alert"
                className="mt-4 rounded-lg border border-red-100 bg-red-50 px-3 py-2 text-sm text-red-700"
              >
                {error}
              </p>
            ) : null}
            <div className="mt-5 grid grid-cols-2 gap-3">
              <button
                type="button"
                onClick={closeConfirmation}
                disabled={cancelling}
                className="rounded-lg border border-slate-200 px-4 py-3 text-sm font-medium text-slate-700 hover:bg-slate-50 disabled:opacity-60"
              >
                Keep waybill
              </button>
              <button
                type="button"
                onClick={() => void confirmCancellation()}
                disabled={cancelling}
                className="rounded-lg bg-red-700 px-4 py-3 text-sm font-medium text-white hover:bg-red-800 disabled:cursor-not-allowed disabled:opacity-60"
              >
                {cancelling ? "Cancelling..." : "Yes, cancel waybill"}
              </button>
            </div>
          </section>
        </div>
      ) : null}
    </>
  );
}
