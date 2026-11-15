"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import {
  ApiError,
  createPark,
  getAgentProfile,
  getParks,
  type CreateParkInput,
  type Park,
} from "@/lib/api";
import { userErrorMessage } from "@/lib/format";

const fieldLimits = {
  id: 64,
  name: 160,
  city: 80,
  state: 80,
} as const;

const emptyForm: CreateParkInput = {
  id: "",
  name: "",
  city: "",
  state: "",
};

function validatePark(form: CreateParkInput): string {
  const labels: Record<keyof CreateParkInput, string> = {
    id: "Park ID",
    name: "Name",
    city: "City",
    state: "State",
  };

  for (const field of Object.keys(fieldLimits) as Array<keyof CreateParkInput>) {
    const value = form[field].trim();
    if (!value) {
      return `${labels[field]} is required.`;
    }
    if (value.length > fieldLimits[field]) {
      return `${labels[field]} must be ${fieldLimits[field]} characters or fewer.`;
    }
  }

  return "";
}

export default function ParksPage() {
  const router = useRouter();
  const [isAdmin, setIsAdmin] = useState<boolean | null>(null);
  const [parks, setParks] = useState<Park[] | null>(null);
  const [form, setForm] = useState<CreateParkInput>(emptyForm);
  const [loading, setLoading] = useState(true);
  const [submitting, setSubmitting] = useState(false);
  const [reloadKey, setReloadKey] = useState(0);
  const [loadError, setLoadError] = useState("");
  const [formError, setFormError] = useState("");
  const [success, setSuccess] = useState("");

  useEffect(() => {
    if (!localStorage.getItem("nurtw_token")) {
      router.replace("/");
      return;
    }

    let active = true;
    async function loadPage() {
      setLoading(true);
      setLoadError("");
      try {
        const profile = await getAgentProfile();
        if (!active) return;
        if (!profile.is_admin) {
          setIsAdmin(false);
          router.replace("/home");
          return;
        }
        setIsAdmin(true);
        setParks(await getParks());
      } catch (requestError) {
        if (!active) return;
        console.error("Could not load admin parks page", requestError);
        if (requestError instanceof ApiError && requestError.status === 401) {
          router.replace("/");
          return;
        }
        setLoadError(
          userErrorMessage(requestError, "Could not load parks. Please try again."),
        );
        setParks(null);
      } finally {
        if (active) setLoading(false);
      }
    }

    void loadPage();
    return () => {
      active = false;
    };
  }, [reloadKey, router]);

  function updateField(field: keyof CreateParkInput, value: string) {
    setForm((current) => ({ ...current, [field]: value }));
    setFormError("");
    setSuccess("");
  }

  async function submitPark(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setFormError("");
    setSuccess("");

    const validationError = validatePark(form);
    if (validationError) {
      setFormError(validationError);
      return;
    }

    setSubmitting(true);
    try {
      const created = await createPark({
        id: form.id.trim(),
        name: form.name.trim(),
        city: form.city.trim(),
        state: form.state.trim(),
      });
      setParks((current) => [...(current ?? []), created.park]);
      setForm(emptyForm);
      setSuccess("Park registered successfully.");
    } catch (requestError) {
      console.error("Could not create park", requestError);
      if (requestError instanceof ApiError && requestError.status === 401) {
        router.replace("/");
        return;
      }
      setFormError(
        requestError instanceof ApiError && requestError.status === 409
          ? requestError.message
          : requestError instanceof ApiError && requestError.status === 403
            ? "Only administrators can register parks."
            : userErrorMessage(requestError, "Could not create park. Please try again."),
      );
    } finally {
      setSubmitting(false);
    }
  }

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
            Park management
          </h1>
          <p className="mt-2 text-sm text-slate-600">
            View active parks and register a new park.
          </p>
        </header>

        {loading ? (
          <section
            aria-busy="true"
            aria-label="Loading parks"
            className="space-y-3 rounded-2xl border border-slate-200 bg-white p-6 shadow-sm"
          >
            <div className="h-5 w-40 animate-pulse rounded bg-slate-200" />
            <div className="h-4 w-full animate-pulse rounded bg-slate-100" />
            <div className="h-4 w-3/4 animate-pulse rounded bg-slate-100" />
          </section>
        ) : loadError ? (
          <div
            role="alert"
            className="rounded-lg border border-red-100 bg-red-50 px-3 py-2 text-sm text-red-700"
          >
            <p>{loadError}</p>
            <button
              type="button"
              onClick={() => setReloadKey((current) => current + 1)}
              className="mt-2 font-medium underline"
            >
              Retry
            </button>
          </div>
        ) : isAdmin === false ? (
          <p className="rounded-2xl border border-slate-200 bg-white p-6 text-sm text-slate-600 shadow-sm">
            Admin access is required to manage parks.
          </p>
        ) : (
          <>
            <section className="rounded-2xl border border-slate-200 bg-white p-5 shadow-sm">
              <h2 className="text-lg font-semibold text-slate-900">Active parks</h2>
              {parks === null ? (
                <p className="mt-3 text-sm text-slate-500">Parks are unavailable.</p>
              ) : parks.length === 0 ? (
                <p className="mt-3 text-sm text-slate-500">
                  No active parks yet. Register the first park below.
                </p>
              ) : (
                <ul className="mt-3 divide-y divide-slate-100">
                  {parks.map((park) => (
                    <li
                      key={park.id}
                      className="flex flex-col gap-1 py-3 first:pt-1 last:pb-1 sm:flex-row sm:items-center sm:justify-between sm:gap-4"
                    >
                      <div>
                        <p className="font-medium text-slate-900">{park.name}</p>
                        <p className="text-sm text-slate-600">{park.city}</p>
                      </div>
                      <code className="break-all text-xs text-slate-500">{park.id}</code>
                    </li>
                  ))}
                </ul>
              )}
            </section>

            <section className="rounded-2xl border border-slate-200 bg-white p-5 shadow-sm">
              <h2 className="text-lg font-semibold text-slate-900">Add a park</h2>
              <form onSubmit={submitPark} className="mt-4 space-y-4">
                {(
                  [
                    ["id", "Park ID"],
                    ["name", "Name"],
                    ["city", "City"],
                    ["state", "State"],
                  ] as const
                ).map(([field, label]) => (
                  <label
                    key={field}
                    className="block text-sm font-medium text-slate-700"
                  >
                    {label}
                    <input
                      name={field}
                      type="text"
                      value={form[field]}
                      onChange={(event) => updateField(field, event.target.value)}
                      maxLength={fieldLimits[field]}
                      required
                      className="mt-1 block w-full rounded-lg border border-slate-300 px-3 py-2.5 text-slate-900 focus:border-emerald-600 focus:outline-none focus:ring-2 focus:ring-emerald-600/30"
                    />
                    <span className="mt-1 block text-xs font-normal text-slate-500">
                      Maximum {fieldLimits[field]} characters
                    </span>
                  </label>
                ))}

                {formError ? (
                  <p
                    role="alert"
                    className="rounded-lg border border-red-100 bg-red-50 px-3 py-2 text-sm text-red-700"
                  >
                    {formError}
                  </p>
                ) : null}
                {success ? (
                  <p
                    role="status"
                    className="rounded-lg border border-emerald-100 bg-emerald-50 px-3 py-2 text-sm text-emerald-800"
                  >
                    {success}
                  </p>
                ) : null}

                <button
                  type="submit"
                  disabled={submitting}
                  className="w-full rounded-lg bg-emerald-700 px-4 py-3 text-sm font-medium text-white transition-colors hover:bg-emerald-800 disabled:cursor-not-allowed disabled:opacity-60 sm:w-auto"
                >
                  {submitting ? "Adding park…" : "Add park"}
                </button>
              </form>
            </section>
          </>
        )}
      </div>
    </main>
  );
}
