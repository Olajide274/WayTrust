const API_BASE_URL = process.env.NEXT_PUBLIC_API_URL || "";

export const AUTH_TOKEN_KEY = "nurtw_token";
const AUTH_CHANGE_EVENT = "nurtw:auth-change";

export const BANK_OPTIONS = [
  { name: "Access Bank", code: "044" },
  { name: "Ecobank", code: "050" },
  { name: "FCMB", code: "214" },
  { name: "Fidelity Bank", code: "070" },
  { name: "First Bank", code: "011" },
  { name: "GTBank", code: "058" },
  { name: "Keystone Bank", code: "082" },
  { name: "Kuda Bank", code: "50211" },
  { name: "Moniepoint MFB", code: "50515" },
  { name: "OPay", code: "999992" },
  { name: "PalmPay", code: "999991" },
  { name: "Polaris Bank", code: "076" },
  { name: "Stanbic IBTC", code: "221" },
  { name: "Sterling Bank", code: "232" },
  ...(process.env.NODE_ENV !== "production"
    ? [{ name: "Test bank (001)", code: "001" }]
    : []),
  { name: "UBA", code: "033" },
  { name: "Union Bank", code: "032" },
  { name: "Wema Bank", code: "035" },
  { name: "Zenith Bank", code: "057" },
];

export class ApiError extends Error {
  constructor(
    message: string,
    public readonly status: number,
    public readonly responseBody?: unknown,
  ) {
    super(message);
    this.name = "ApiError";
  }
}

export type AgentProfile = {
  full_name?: string;
  phone_number?: string;
  park_id?: string;
  wallet_balance_kobo: number;
  is_admin?: boolean;
};

export type Park = {
  id: string;
  name: string;
  city: string;
};

export type AgentWaybill = {
  id: string;
  agent_id: string;
  sender_phone: string;
  receiver_phone: string;
  driver_phone: string;
  vehicle_reg_number: string;
  origin_park_id: string;
  destination_park_id: string;
  total_freight_kobo: number;
  current_state: string;
  driver_verified_name: string;
  created_at: string;
  driver_payout_kobo: number;
  agent_commission_kobo: number;
  union_dues_kobo: number;
  platform_treasury_kobo: number;
};

export type WaybillStatus = {
  waybill_id: string;
  current_state: string;
  origin_park_id: string;
  destination_park_id: string;
  total_freight_kobo: number;
  total_freight_naira: number;
  driver_phone: string;
  receiver_phone: string;
  vehicle_reg_number: string;
  failed_pin_attempts: number;
  created_at: string;
  updated_at: string;
};

export type CreateWaybillInput = {
  sender_phone: string;
  receiver_phone: string;
  driver_phone: string;
  vehicle_reg_number: string;
  origin_park_id: string;
  destination_park_id: string;
  total_freight_kobo: number;
  driver_account_number: string;
  driver_bank_code: string;
  driver_declared_name: string;
};

export type ResolveBankAccountInput = {
  account_number: string;
  bank_code: string;
  declared_name: string;
};

export type BankAccountResolution = {
  verified: boolean;
  resolved_name: string;
  confidence_score: number;
  message?: string;
};

export type CreatedWaybill = {
  waybill_id: string;
  secret_pin: string;
  agent_remaining_balance_kobo: number;
};

type CreateWaybillResponse = {
  WaybillID: string;
  SecretPin: string;
  AgentRemainingBalance: number;
};

export const PROFILE_REFRESH_EVENT = "nurtw:profile-refresh";

export function hasAuthToken(): boolean {
  return (
    typeof window !== "undefined" &&
    Boolean(window.localStorage.getItem(AUTH_TOKEN_KEY))
  );
}

export function subscribeToAuthChanges(onChange: () => void): () => void {
  if (typeof window === "undefined") {
    return () => {};
  }

  window.addEventListener("storage", onChange);
  window.addEventListener(AUTH_CHANGE_EVENT, onChange);

  return () => {
    window.removeEventListener("storage", onChange);
    window.removeEventListener(AUTH_CHANGE_EVENT, onChange);
  };
}

function getErrorMessage(payload: unknown, fallback: string): string {
  if (
    typeof payload === "object" &&
    payload !== null &&
    "error" in payload &&
    typeof payload.error === "string"
  ) {
    return payload.error;
  }

  if (
    typeof payload === "object" &&
    payload !== null &&
    "message" in payload &&
    typeof payload.message === "string"
  ) {
    return payload.message;
  }

  return fallback;
}

export async function apiRequest<T>(
  path: string,
  init: RequestInit = {},
  options: { clearAuthOnUnauthorized?: boolean } = {},
): Promise<T> {
  const headers = new Headers(init.headers);
  const token =
    typeof window === "undefined"
      ? null
      : window.localStorage.getItem(AUTH_TOKEN_KEY);

  if (token) {
    headers.set("Authorization", `Bearer ${token}`);
  }

  const response = await fetch(
    `${API_BASE_URL.replace(/\/+$/, "")}${path}`,
    { ...init, headers },
  );
  const responseText = await response.text();
  let payload: unknown;

  if (responseText) {
    try {
      payload = JSON.parse(responseText);
    } catch {
      if (response.ok) {
        throw new ApiError(
          "The server returned an invalid response",
          response.status,
        );
      }
      payload = responseText;
    }
  }

  if (!response.ok) {
    if (
      response.status === 401 &&
      options.clearAuthOnUnauthorized !== false &&
      typeof window !== "undefined"
    ) {
      window.localStorage.removeItem(AUTH_TOKEN_KEY);
      window.dispatchEvent(new Event(AUTH_CHANGE_EVENT));
    }

    throw new ApiError(
      getErrorMessage(payload, `Request failed (${response.status})`),
      response.status,
      payload,
    );
  }

  return payload as T;
}

export async function loginAgent(
  phone_number: string,
  password: string,
): Promise<{
  message: string;
  token: string;
  agent: AgentProfile;
  is_admin: boolean;
}> {
  return apiRequest(
    "/api/v1/agent/login",
    {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ phone_number, password }),
    },
    { clearAuthOnUnauthorized: false },
  );
}

export async function verifyWaybillPin(
  waybill_id: string,
  pin: string,
): Promise<{
  verified: boolean;
  waybill_id?: string;
  state?: string;
  message?: string;
  attempts_remaining?: number;
}> {
  return apiRequest(
    "/api/v1/waybills/verify-pin",
    {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ waybill_id, pin }),
    },
    { clearAuthOnUnauthorized: false },
  );
}

function isBankAccountResolution(
  payload: unknown,
): payload is BankAccountResolution {
  return (
    typeof payload === "object" &&
    payload !== null &&
    "verified" in payload &&
    typeof payload.verified === "boolean" &&
    "resolved_name" in payload &&
    typeof payload.resolved_name === "string" &&
    "confidence_score" in payload &&
    typeof payload.confidence_score === "number" &&
    (!("message" in payload) || typeof payload.message === "string")
  );
}

export async function resolveBankAccount(
  input: ResolveBankAccountInput,
): Promise<BankAccountResolution> {
  try {
    return await apiRequest<BankAccountResolution>("/api/v1/bank/resolve", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(input),
    });
  } catch (requestError) {
    if (
      requestError instanceof ApiError &&
      requestError.status === 422 &&
      isBankAccountResolution(requestError.responseBody)
    ) {
      return requestError.responseBody;
    }
    throw requestError;
  }
}

export function getAgentProfile(): Promise<AgentProfile> {
  return apiRequest<AgentProfile>("/api/v1/agent/profile");
}

export function getParks(): Promise<Park[]> {
  return apiRequest<Park[]>("/api/v1/parks");
}

export function getAgentWaybills(): Promise<AgentWaybill[]> {
  return apiRequest<AgentWaybill[]>("/api/v1/agent/waybills");
}

export function getWaybillStatus(waybillId: string): Promise<WaybillStatus> {
  const params = new URLSearchParams({ id: waybillId });
  return apiRequest<WaybillStatus>(`/api/v1/waybills/status?${params}`);
}

export function createWaybill(
  input: CreateWaybillInput,
): Promise<CreatedWaybill> {
  return apiRequest<CreateWaybillResponse>("/api/v1/waybills", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  }).then((response) => ({
    waybill_id: response.WaybillID,
    secret_pin: response.SecretPin,
    agent_remaining_balance_kobo: response.AgentRemainingBalance,
  }));
}
