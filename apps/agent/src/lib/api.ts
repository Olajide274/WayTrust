const API_BASE_URL = process.env.NEXT_PUBLIC_API_URL || "";

export const AUTH_TOKEN_KEY = "nurtw_token";
const AUTH_CHANGE_EVENT = "nurtw:auth-change";

export class ApiError extends Error {
  constructor(
    message: string,
    public readonly status: number,
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

  return fallback;
}

export async function apiRequest<T>(
  path: string,
  init: RequestInit = {},
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
    if (response.status === 401 && typeof window !== "undefined") {
      window.localStorage.removeItem(AUTH_TOKEN_KEY);
      window.dispatchEvent(new Event(AUTH_CHANGE_EVENT));
    }

    throw new ApiError(
      getErrorMessage(payload, `Request failed (${response.status})`),
      response.status,
    );
  }

  return payload as T;
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
