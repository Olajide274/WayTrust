import type { Park } from "@/lib/api";

const STATUS_STYLES = {
  created: "border-slate-200 bg-slate-50 text-slate-600",
  booked: "border-amber-200 bg-amber-50 text-amber-800",
  transit: "border-sky-200 bg-sky-50 text-sky-800",
  delivered: "border-emerald-200 bg-emerald-50 text-emerald-800",
  paid: "border-teal-200 bg-teal-50 text-teal-900",
  dispute: "border-red-200 bg-red-50 text-red-800",
  unclaimed: "border-orange-200 bg-orange-50 text-orange-800",
  refunded: "border-violet-200 bg-violet-50 text-violet-800",
  cancelled: "border-slate-200 bg-slate-100 text-slate-600",
} as const;

export function formatPark(id: string | undefined, parks: readonly Park[]): string {
  if (!id) return "Park not set";
  const park = parks.find((item) => item.id === id);
  if (!park) return "Park not set";
  return park.city ? `${park.name}, ${park.city}` : park.name;
}

export function formatStatus(state: string): {
  label: string;
  className: string;
} {
  switch (state) {
    case "CREATED":
      return { label: "Created", className: STATUS_STYLES.created };
    case "ESCROW_LOCKED":
      return { label: "Booked", className: STATUS_STYLES.booked };
    case "IN_TRANSIT":
      return { label: "In transit", className: STATUS_STYLES.transit };
    case "DELIVERED":
      return { label: "Delivered", className: STATUS_STYLES.delivered };
    case "DISBURSED":
      return { label: "Paid out", className: STATUS_STYLES.paid };
    case "DISPUTE_HOLD":
      return { label: "In dispute", className: STATUS_STYLES.dispute };
    case "UNCLAIMED_HOLD":
      return { label: "Unclaimed", className: STATUS_STYLES.unclaimed };
    case "REFUNDED":
      return { label: "Refunded", className: STATUS_STYLES.refunded };
    case "CANCELLED":
      return { label: "Cancelled", className: STATUS_STYLES.cancelled };
    default: {
      const label = state
        .trim()
        .replace(/[_-]+/g, " ")
        .toLowerCase();
      return {
        label: label ? `${label[0].toUpperCase()}${label.slice(1)}` : "Unknown status",
        className: STATUS_STYLES.cancelled,
      };
    }
  }
}

export function formatNaira(kobo: number | null | undefined): string {
  return (Number(kobo || 0) / 100).toLocaleString("en-NG", {
    style: "currency",
    currency: "NGN",
  });
}

export function formatDate(value: string): string {
  const date = new Date(value);
  return Number.isNaN(date.getTime())
    ? "Date unavailable"
    : date.toLocaleString("en-NG", {
        dateStyle: "medium",
        timeStyle: "short",
      });
}

export function userErrorMessage(error: unknown, fallback: string): string {
  if (error instanceof Error && "status" in error) {
    const status = error.status;
    if (status === 401) return "Your session has expired. Please sign in again.";
    if (status === 403) return "You don't have permission to do that.";
    if (status === 404) return "We couldn't find that information.";
    if (status === 409) return "This information has changed. Please refresh and try again.";
    if (status === 429) return "Please wait a moment and try again.";
    if (typeof status === "number" && status >= 500) {
      return "Something went wrong. Please try again.";
    }
  }
  return fallback;
}

export function formatPINVerification(state: string | undefined): string {
  return `PIN verified. Delivery marked ${formatStatus(state || "DELIVERED").label.toLowerCase()}.`;
}
