import { Buffer } from "buffer";
import { getToken } from "./storage";

export interface JwtPayload {
  uid?: number;
  ver?: number;
  sub?: string;
  exp?: number; // Unix timestamp in seconds
  iat?: number; // Unix timestamp in seconds
  iss?: string;
}

export interface StoredTokenValidation {
  valid: boolean;
  expiringSoon: boolean;
  remainingMs: number;
  payload: JwtPayload | null;
  token: string | null;
}

/** Default threshold for considering a token expiring soon: 24 hours */
export const DEFAULT_EXPIRY_THRESHOLD_MS = 24 * 60 * 60 * 1000;

/**
 * Decodes the base64url payload segment of a JWT string.
 * Returns null if the token format is invalid or cannot be parsed as JSON.
 */
export function decodeJwtPayload(token: string | null | undefined): JwtPayload | null {
  if (!token || typeof token !== "string") return null;

  try {
    const parts = token.split(".");
    if (parts.length !== 3) return null;

    const base64Url = parts[1];
    if (!base64Url) return null;

    const base64 = base64Url.replace(/-/g, "+").replace(/_/g, "/");
    const jsonStr = Buffer.from(base64, "base64").toString("utf8");
    const payload = JSON.parse(jsonStr);

    if (typeof payload !== "object" || payload === null) {
      return null;
    }

    return payload as JwtPayload;
  } catch {
    return null;
  }
}

/**
 * Returns the remaining lifetime in milliseconds before the token expires.
 * Returns 0 (or negative) if the token is already expired or invalid.
 */
export function getTokenRemainingMs(token: string | null | undefined, nowMs: number = Date.now()): number {
  const payload = decodeJwtPayload(token);
  if (!payload || typeof payload.exp !== "number") {
    return 0;
  }

  const expiryMs = payload.exp * 1000;
  return Math.max(0, expiryMs - nowMs);
}

/**
 * Checks if the JWT is already expired.
 */
export function isTokenExpired(token: string | null | undefined, nowMs: number = Date.now()): boolean {
  const payload = decodeJwtPayload(token);
  if (!payload || typeof payload.exp !== "number") {
    return true;
  }

  const expiryMs = payload.exp * 1000;
  return nowMs >= expiryMs;
}

/**
 * Checks if the JWT expires within the given threshold (default 24 hours).
 * Returns true if expired or expiring within thresholdMs.
 */
export function isTokenExpiringSoon(
  token: string | null | undefined,
  thresholdMs: number = DEFAULT_EXPIRY_THRESHOLD_MS,
  nowMs: number = Date.now()
): boolean {
  const payload = decodeJwtPayload(token);
  if (!payload || typeof payload.exp !== "number") {
    return true;
  }

  const expiryMs = payload.exp * 1000;
  return expiryMs - nowMs <= thresholdMs;
}

/**
 * Validates the currently stored authentication token from SecureStore.
 */
export async function validateStoredToken(
  thresholdMs: number = DEFAULT_EXPIRY_THRESHOLD_MS
): Promise<StoredTokenValidation> {
  const token = await getToken();
  if (!token) {
    return {
      valid: false,
      expiringSoon: true,
      remainingMs: 0,
      payload: null,
      token: null,
    };
  }

  const payload = decodeJwtPayload(token);
  const now = Date.now();
  const remainingMs = getTokenRemainingMs(token, now);
  const expired = isTokenExpired(token, now);
  const expiringSoon = isTokenExpiringSoon(token, thresholdMs, now);

  return {
    valid: !expired && payload !== null,
    expiringSoon,
    remainingMs,
    payload,
    token,
  };
}
