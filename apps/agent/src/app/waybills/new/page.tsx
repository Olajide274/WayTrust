"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { PRODUCT_NAME } from "@/lib/branding";
import {
  ApiError,
  BANK_OPTIONS,
  createWaybill,
  getParks,
  hasAuthToken,
  PROFILE_REFRESH_EVENT,
  resolveBankAccount,
  type BankAccountResolution,
  type CreatedWaybill,
  type Park,
} from "@/lib/api";

type FormValues = {
  senderPhone: string;
  receiverPhone: string;
  driverPhone: string;
  vehicleRegNumber: string;
  originParkId: string;
  destinationParkId: string;
  freightNaira: string;
  driverAccountNumber: string;
  driverBankCode: string;
  driverDeclaredName: string;
};

const initialForm: FormValues = {
  senderPhone: "",
  receiverPhone: "",
  driverPhone: "",
  vehicleRegNumber: "",
  originParkId: "",
  destinationParkId: "",
  freightNaira: "",
  driverAccountNumber: "",
  driverBankCode: "",
  driverDeclaredName: "",
};

function naira(kobo: number): string {
  return (kobo / 100).toLocaleString("en-NG", {
    style: "currency",
    currency: "NGN",
  });
}

function getBankDetailsKey(
  form: Pick<
    FormValues,
    "driverAccountNumber" | "driverBankCode" | "driverDeclaredName"
  >,
): string {
  return JSON.stringify([
    form.driverAccountNumber.trim(),
    form.driverBankCode.trim(),
    form.driverDeclaredName.trim(),
  ]);
}

function validateForm(
  form: FormValues,
): { message: string; freightKobo?: number } {
  const requiredFields: Array<[string, string]> = [
    [form.senderPhone, "Sender phone number"],
    [form.receiverPhone, "Receiver phone number"],
    [form.driverPhone, "Driver phone number"],
    [form.vehicleRegNumber, "Vehicle registration"],
    [form.originParkId, "Origin park"],
    [form.destinationParkId, "Destination park"],
    [form.freightNaira, "Freight amount"],
    [form.driverAccountNumber, "Driver bank account number"],
    [form.driverBankCode, "Driver bank code"],
    [form.driverDeclaredName, "Driver declared name"],
  ];
  const missing = requiredFields.find(([value]) => !value.trim());

  if (missing) {
    return { message: `${missing[1]} is required.` };
  }

  const phones = [form.senderPhone, form.receiverPhone, form.driverPhone];
  if (
    phones.some((phone) => {
      const digits = phone.replace(/\D/g, "");
      return (
        !/^\+?[0-9][0-9 ()-]*$/.test(phone.trim()) ||
        digits.length < 10 ||
        digits.length > 15
      );
    })
  ) {
    return { message: "Enter valid phone numbers with 10 to 15 digits." };
  }

  if (form.originParkId === form.destinationParkId) {
    return { message: "Origin and destination parks must be different." };
  }

  const freight = form.freightNaira.trim();
  if (!/^\d+(?:\.\d{1,2})?$/.test(freight)) {
    return {
      message:
        "Enter a valid freight amount in naira (up to 2 decimal places).",
    };
  }
  const [nairaPart, koboPart = ""] = freight.split(".");
  const freightKobo =
    Number(nairaPart) * 100 + Number(koboPart.padEnd(2, "0"));
  if (!Number.isSafeInteger(freightKobo) || freightKobo <= 0) {
    return { message: "Freight amount must be greater than zero." };
  }

  if (!/^\d{10}$/.test(form.driverAccountNumber.trim())) {
    return { message: "Enter a valid 10-digit driver bank account number." };
  }
  if (!/^\d{3,6}$/.test(form.driverBankCode.trim())) {
    return { message: "Enter a valid numeric bank code." };
  }

  return { message: "", freightKobo };
}

function Field({
  label,
  name,
  value,
  onChange,
  type = "text",
  inputMode,
  placeholder,
}: {
  label: string;
  name: keyof FormValues;
  value: string;
  onChange: (name: keyof FormValues, value: string) => void;
  type?: string;
  inputMode?: "decimal" | "numeric" | "tel" | "text";
  placeholder?: string;
}) {
  return (
    <div>
      <label
        htmlFor={name}
        className="mb-1.5 block text-sm font-medium text-slate-700"
      >
        {label}
      </label>
      <input
        id={name}
        name={name}
        type={type}
        inputMode={inputMode}
        value={value}
        onChange={(event) => onChange(name, event.target.value)}
        placeholder={placeholder}
        className="w-full rounded-lg border border-slate-300 px-3 py-2.5 text-slate-900 placeholder:text-slate-400 focus:border-emerald-600 focus:outline-none focus:ring-2 focus:ring-emerald-600/30"
        required
      />
    </div>
  );
}

export default function NewWaybillPage() {
  const router = useRouter();
  const [parks, setParks] = useState<Park[]>([]);
  const [parksLoading, setParksLoading] = useState(true);
  const [parksError, setParksError] = useState("");
  const [form, setForm] = useState<FormValues>(initialForm);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState("");
  const [resolvingAccount, setResolvingAccount] = useState(false);
  const [accountResolution, setAccountResolution] = useState<{
    key: string;
    result: BankAccountResolution;
  } | null>(null);
  const [accountResolutionError, setAccountResolutionError] = useState<{
    key: string;
    message: string;
  } | null>(null);
  const [result, setResult] = useState<CreatedWaybill | null>(null);
  const [copied, setCopied] = useState(false);
  const [copyError, setCopyError] = useState("");

  async function loadParks() {
    setParksLoading(true);
    setParksError("");
    try {
      setParks(await getParks());
    } catch (requestError) {
      if (requestError instanceof ApiError && requestError.status === 401) {
        router.replace("/");
        return;
      }
      setParksError(
        requestError instanceof Error
          ? requestError.message
          : "Could not load parks.",
      );
    } finally {
      setParksLoading(false);
    }
  }

  useEffect(() => {
    if (!hasAuthToken()) {
      router.replace("/");
      return;
    }

    let active = true;
    getParks()
      .then((loadedParks) => {
        if (active) setParks(loadedParks);
      })
      .catch((requestError: unknown) => {
        if (!active) return;
        if (requestError instanceof ApiError && requestError.status === 401) {
          router.replace("/");
          return;
        }
        setParksError(
          requestError instanceof Error
            ? requestError.message
            : "Could not load parks.",
        );
      })
      .finally(() => {
        if (active) setParksLoading(false);
      });

    return () => {
      active = false;
    };
  }, [router]);

  function updateField(name: keyof FormValues, value: string) {
    setForm((current) => ({ ...current, [name]: value }));
    if (
      name === "driverAccountNumber" ||
      name === "driverBankCode" ||
      name === "driverDeclaredName"
    ) {
      setAccountResolution(null);
      setAccountResolutionError(null);
    }
  }

  async function verifyAccount() {
    const key = getBankDetailsKey(form);
    const accountNumber = form.driverAccountNumber.trim();
    const bankCode = form.driverBankCode.trim();
    const declaredName = form.driverDeclaredName.trim();
    setAccountResolution(null);
    setAccountResolutionError(null);

    if (!/^\d{10}$/.test(accountNumber)) {
      setAccountResolutionError({
        key,
        message: "Enter a valid 10-digit driver bank account number.",
      });
      return;
    }
    if (!bankCode) {
      setAccountResolutionError({ key, message: "Select a driver bank." });
      return;
    }
    if (!declaredName) {
      setAccountResolutionError({
        key,
        message: "Enter the driver's declared account name.",
      });
      return;
    }

    setResolvingAccount(true);
    try {
      const resolution = await resolveBankAccount({
        account_number: accountNumber,
        bank_code: bankCode,
        declared_name: declaredName,
      });
      setAccountResolution({ key, result: resolution });
    } catch (requestError) {
      setAccountResolutionError({
        key,
        message:
          requestError instanceof Error
            ? requestError.message
            : "Please check the account details and try again.",
      });
    } finally {
      setResolvingAccount(false);
    }
  }

  async function onSubmit(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setError("");
    const validation = validateForm(form);
    if (validation.message || validation.freightKobo === undefined) {
      setError(validation.message);
      return;
    }

    setSubmitting(true);
    try {
      const created = await createWaybill({
        sender_phone: form.senderPhone.trim(),
        receiver_phone: form.receiverPhone.trim(),
        driver_phone: form.driverPhone.trim(),
        vehicle_reg_number: form.vehicleRegNumber.trim(),
        origin_park_id: form.originParkId,
        destination_park_id: form.destinationParkId,
        total_freight_kobo: validation.freightKobo,
        driver_account_number: form.driverAccountNumber.trim(),
        driver_bank_code: form.driverBankCode.trim(),
        driver_declared_name: form.driverDeclaredName.trim(),
      });
      setResult(created);
      window.dispatchEvent(new Event(PROFILE_REFRESH_EVENT));
    } catch (requestError) {
      if (requestError instanceof ApiError && requestError.status === 401) {
        router.replace("/");
        return;
      }
      setError(
        requestError instanceof ApiError && requestError.status === 402
          ? "Insufficient wallet balance. Fund the agent wallet before booking this waybill."
          : requestError instanceof Error
            ? `Could not book waybill: ${requestError.message}`
            : "Could not book waybill. Please try again.",
      );
    } finally {
      setSubmitting(false);
    }
  }

  async function copyPin() {
    if (!result) return;
    setCopyError("");
    try {
      await navigator.clipboard.writeText(result.secret_pin);
      setCopied(true);
    } catch {
      setCopied(false);
      setCopyError("Could not copy the PIN. Please select and copy it manually.");
    }
  }

  return (
    <main className="min-h-screen bg-slate-50 px-4 py-8">
      <div className="mx-auto max-w-lg space-y-5">
        <div>
          <Link
            href="/home"
            className="text-sm font-medium text-emerald-700 hover:text-emerald-800"
          >
            ← Back to home
          </Link>
          <p className="mt-5 text-xs font-medium uppercase tracking-wide text-emerald-700">
            {PRODUCT_NAME}
          </p>
          <h1 className="mt-1 text-2xl font-semibold text-slate-900">
            Book waybill
          </h1>
          <p className="mt-1 text-sm text-slate-500">
            Enter the shipment details to create an escrow-backed waybill.
          </p>
        </div>

        {result ? (
          <section
            aria-live="polite"
            className="space-y-4 rounded-2xl border border-emerald-200 bg-white p-6 shadow-sm"
          >
            <div>
              <p className="text-sm font-medium text-emerald-700">
                Waybill booked successfully
              </p>
              <p className="mt-1 text-xs text-slate-500">Waybill ID</p>
              <p className="break-all text-lg font-semibold text-slate-900">
                {result.waybill_id}
              </p>
            </div>
            <div className="rounded-xl border border-amber-200 bg-amber-50 p-4">
              <p className="text-xs font-semibold uppercase tracking-wide text-amber-900">
                Secret PIN — share securely with the receiver
              </p>
              <div className="mt-2 flex items-center justify-between gap-3">
                <p className="text-2xl font-bold tracking-[0.2em] text-amber-950">
                  {result.secret_pin}
                </p>
                <button
                  type="button"
                  onClick={copyPin}
                  className="shrink-0 rounded-lg border border-amber-300 bg-white px-3 py-2 text-sm font-medium text-amber-950 hover:bg-amber-100"
                >
                  {copied ? "Copied" : "Copy PIN"}
                </button>
              </div>
              {copyError ? (
                <p className="mt-2 text-sm text-red-700">{copyError}</p>
              ) : null}
            </div>
            <div>
              <p className="text-sm text-slate-500">Remaining wallet balance</p>
              <p className="text-xl font-semibold text-slate-900">
                {naira(result.agent_remaining_balance_kobo)}
              </p>
            </div>
            <Link
              href="/home"
              className="inline-flex w-full items-center justify-center rounded-lg bg-emerald-700 px-4 py-3 text-sm font-medium text-white hover:bg-emerald-800"
            >
              Return to home
            </Link>
          </section>
        ) : (
          <form
            onSubmit={onSubmit}
            className="space-y-5 rounded-2xl border border-slate-200 bg-white p-5 shadow-sm sm:p-6"
          >
            <section className="space-y-4">
              <h2 className="text-sm font-semibold uppercase tracking-wide text-slate-600">
                Shipment
              </h2>
              <Field
                label="Sender phone"
                name="senderPhone"
                type="tel"
                inputMode="tel"
                value={form.senderPhone}
                onChange={updateField}
                placeholder="eg. 0803..."
              />
              <Field
                label="Receiver phone"
                name="receiverPhone"
                type="tel"
                inputMode="tel"
                value={form.receiverPhone}
                onChange={updateField}
                placeholder="eg. 0803..."
              />
              <Field
                label="Driver phone"
                name="driverPhone"
                type="tel"
                inputMode="tel"
                value={form.driverPhone}
                onChange={updateField}
                placeholder="eg. 0803..."
              />
              <Field
                label="Vehicle registration"
                name="vehicleRegNumber"
                value={form.vehicleRegNumber}
                onChange={updateField}
                placeholder="eg. ABC 123 XY"
              />
              {parksError ? (
                <div className="rounded-lg border border-red-100 bg-red-50 px-3 py-2 text-sm text-red-700">
                  <p>Could not load parks: {parksError}</p>
                  <button
                    type="button"
                    onClick={() => void loadParks()}
                    className="mt-1 font-medium underline"
                  >
                    Try again
                  </button>
                </div>
              ) : parksLoading ? (
                <p className="text-sm text-slate-500">Loading parks…</p>
              ) : parks.length === 0 ? (
                <p className="rounded-lg border border-slate-200 bg-slate-50 px-3 py-2 text-sm text-slate-600">
                  No active parks are available. Please try again later.
                </p>
              ) : (
                <>
                  {(
                    [
                      ["originParkId", "Origin park"],
                      ["destinationParkId", "Destination park"],
                    ] as const
                  ).map(([name, label]) => (
                    <div key={name}>
                      <label
                        htmlFor={name}
                        className="mb-1.5 block text-sm font-medium text-slate-700"
                      >
                        {label}
                      </label>
                      <select
                        id={name}
                        name={name}
                        value={form[name]}
                        onChange={(event) => updateField(name, event.target.value)}
                        className="w-full rounded-lg border border-slate-300 bg-white px-3 py-2.5 text-slate-900 focus:border-emerald-600 focus:outline-none focus:ring-2 focus:ring-emerald-600/30"
                        required
                      >
                        <option value="">Select a park</option>
                        {parks.map((park) => (
                          <option key={park.id} value={park.id}>
                            {park.name}{park.city ? ` — ${park.city}` : ""}
                          </option>
                        ))}
                      </select>
                    </div>
                  ))}
                </>
              )}
            </section>

            <section className="space-y-4 border-t border-slate-100 pt-5">
              <h2 className="text-sm font-semibold uppercase tracking-wide text-slate-600">
                Freight and driver payout
              </h2>
              <Field
                label="Freight amount (naira)"
                name="freightNaira"
                inputMode="decimal"
                value={form.freightNaira}
                onChange={updateField}
                placeholder="e.g. 25000.00"
              />
              <Field
                label="Driver bank account number"
                name="driverAccountNumber"
                inputMode="numeric"
                value={form.driverAccountNumber}
                onChange={updateField}
                placeholder="10-digit account number"
              />
              {BANK_OPTIONS.length === 0 ? (
                <p
                  role="status"
                  className="rounded-lg border border-amber-200 bg-amber-50 px-3 py-2 text-sm text-amber-900"
                >
                  No banks are available. Please contact support before
                  booking.
                </p>
              ) : (
                <div>
                  <label
                    htmlFor="driverBankCode"
                    className="mb-1.5 block text-sm font-medium text-slate-700"
                  >
                    Driver bank
                  </label>
                  <select
                    id="driverBankCode"
                    name="driverBankCode"
                    value={form.driverBankCode}
                    onChange={(event) =>
                      updateField("driverBankCode", event.target.value)
                    }
                    className="w-full rounded-lg border border-slate-300 bg-white px-3 py-2.5 text-slate-900 focus:border-emerald-600 focus:outline-none focus:ring-2 focus:ring-emerald-600/30"
                    required
                  >
                    <option value="">Select a bank</option>
                    {BANK_OPTIONS.map((bank) => (
                      <option key={bank.code} value={bank.code}>
                        {bank.name}
                      </option>
                    ))}
                  </select>
                </div>
              )}
              <Field
                label="Driver declared name"
                name="driverDeclaredName"
                value={form.driverDeclaredName}
                onChange={updateField}
                placeholder="Name on driver's bank account"
              />
              <div className="space-y-2">
                <button
                  type="button"
                  onClick={() => void verifyAccount()}
                  disabled={resolvingAccount}
                  className="w-full rounded-lg border border-emerald-700 px-4 py-2.5 text-sm font-medium text-emerald-800 transition-colors hover:bg-emerald-50 disabled:cursor-not-allowed disabled:opacity-60"
                >
                  {resolvingAccount ? "Verifying account…" : "Verify account"}
                </button>
                {accountResolutionError?.key === getBankDetailsKey(form) ? (
                  <p
                    role="alert"
                    className="rounded-lg border border-red-100 bg-red-50 px-3 py-2 text-sm text-red-700"
                  >
                    Could not resolve this account:{" "}
                    {accountResolutionError.message}
                  </p>
                ) : accountResolution?.key === getBankDetailsKey(form) ? (
                  <div
                    role={accountResolution.result.verified ? "status" : "alert"}
                    className={`rounded-lg border px-3 py-2 text-sm ${
                      accountResolution.result.verified
                        ? "border-emerald-200 bg-emerald-50 text-emerald-900"
                        : "border-red-100 bg-red-50 text-red-700"
                    }`}
                  >
                    {!accountResolution.result.verified ? (
                      <p>
                        Could not resolve this account
                        {accountResolution.result.message
                          ? `: ${accountResolution.result.message}`
                          : "."}
                      </p>
                    ) : null}
                    <p>
                      Resolved account name:{" "}
                      {accountResolution.result.resolved_name || "Not provided"}
                    </p>
                    <p>
                      Match result:{" "}
                      {accountResolution.result.verified
                        ? "Matches the declared name"
                        : "Not verified"}
                    </p>
                  </div>
                ) : (
                  <p role="status" className="text-sm text-slate-500">
                    Account verification is optional and does not prevent
                    booking.
                  </p>
                )}
              </div>
            </section>

            {error ? (
              <p
                role="alert"
                className="rounded-lg border border-red-100 bg-red-50 px-3 py-2 text-sm text-red-700"
              >
                {error}
              </p>
            ) : null}

            <button
              type="submit"
              disabled={
                submitting ||
                parksLoading ||
                Boolean(parksError) ||
                parks.length === 0
              }
              className="w-full rounded-lg bg-emerald-700 px-4 py-3 text-sm font-medium text-white transition-colors hover:bg-emerald-800 disabled:cursor-not-allowed disabled:opacity-60"
            >
              {submitting ? "Booking waybill…" : "Book waybill"}
            </button>
          </form>
        )}
      </div>
    </main>
  );
}
