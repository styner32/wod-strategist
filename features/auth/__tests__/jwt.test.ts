import { Buffer } from "buffer";
import {
  decodeJwtPayload,
  getTokenRemainingMs,
  isTokenExpired,
  isTokenExpiringSoon,
  validateStoredToken,
} from "../jwt";
import * as storage from "../storage";
import { useAuthStore } from "../useAuthStore";

// Helper to create mock JWTs with given payload
function createMockJwt(payload: Record<string, unknown>): string {
  const header = Buffer.from(JSON.stringify({ alg: "HS256", typ: "JWT" })).toString("base64url");
  const body = Buffer.from(JSON.stringify(payload)).toString("base64url");
  const signature = "mock_signature";
  return `${header}.${body}.${signature}`;
}

jest.mock("../storage", () => ({
  getToken: jest.fn(),
  getUserID: jest.fn(),
  setToken: jest.fn().mockResolvedValue(undefined),
  setUserID: jest.fn().mockResolvedValue(undefined),
  clearToken: jest.fn().mockResolvedValue(undefined),
  clearUserID: jest.fn().mockResolvedValue(undefined),
}));

describe("features/auth/jwt", () => {
  const nowSec = 1700000000;
  const nowMs = nowSec * 1000;

  beforeEach(() => {
    jest.clearAllMocks();
  });

  describe("decodeJwtPayload", () => {
    it("decodes a valid JWT payload correctly", () => {
      const token = createMockJwt({
        uid: 42,
        ver: 1,
        sub: "testuser",
        exp: nowSec + 3600,
      });

      const payload = decodeJwtPayload(token);
      expect(payload).toEqual({
        uid: 42,
        ver: 1,
        sub: "testuser",
        exp: nowSec + 3600,
      });
    });

    it("returns null for null, undefined, or empty token", () => {
      expect(decodeJwtPayload(null)).toBeNull();
      expect(decodeJwtPayload(undefined)).toBeNull();
      expect(decodeJwtPayload("")).toBeNull();
    });

    it("returns null for malformed tokens", () => {
      expect(decodeJwtPayload("invalid-token")).toBeNull();
      expect(decodeJwtPayload("header.body")).toBeNull();
      expect(decodeJwtPayload("header.not-json.sig")).toBeNull();
    });
  });

  describe("isTokenExpired", () => {
    it("returns false for token expiring in the future", () => {
      const token = createMockJwt({ exp: nowSec + 3600 });
      expect(isTokenExpired(token, nowMs)).toBe(false);
    });

    it("returns true for token expired in the past", () => {
      const token = createMockJwt({ exp: nowSec - 10 });
      expect(isTokenExpired(token, nowMs)).toBe(true);
    });

    it("returns true for exactly expired timestamp", () => {
      const token = createMockJwt({ exp: nowSec });
      expect(isTokenExpired(token, nowMs)).toBe(true);
    });

    it("returns true for invalid or missing token", () => {
      expect(isTokenExpired(null, nowMs)).toBe(true);
      expect(isTokenExpired("invalid", nowMs)).toBe(true);
    });
  });

  describe("isTokenExpiringSoon", () => {
    const ONE_DAY_MS = 24 * 60 * 60 * 1000;

    it("returns false when expiration is well beyond threshold (e.g. 5 days)", () => {
      const token = createMockJwt({ exp: nowSec + 5 * 24 * 3600 });
      expect(isTokenExpiringSoon(token, ONE_DAY_MS, nowMs)).toBe(false);
    });

    it("returns true when token expires in less than 24 hours (e.g. 12 hours)", () => {
      const token = createMockJwt({ exp: nowSec + 12 * 3600 });
      expect(isTokenExpiringSoon(token, ONE_DAY_MS, nowMs)).toBe(true);
    });

    it("returns true when token is already expired", () => {
      const token = createMockJwt({ exp: nowSec - 100 });
      expect(isTokenExpiringSoon(token, ONE_DAY_MS, nowMs)).toBe(true);
    });
  });

  describe("getTokenRemainingMs", () => {
    it("returns correct remaining milliseconds for future token", () => {
      const token = createMockJwt({ exp: nowSec + 60 });
      expect(getTokenRemainingMs(token, nowMs)).toBe(60000);
    });

    it("returns 0 for expired token", () => {
      const token = createMockJwt({ exp: nowSec - 60 });
      expect(getTokenRemainingMs(token, nowMs)).toBe(0);
    });
  });

  describe("validateStoredToken", () => {
    it("returns invalid when no token is in storage", async () => {
      (storage.getToken as jest.Mock).mockResolvedValue(null);

      const result = await validateStoredToken();
      expect(result.valid).toBe(false);
      expect(result.expiringSoon).toBe(true);
      expect(result.token).toBeNull();
    });

    it("returns valid and not expiring soon for fresh token", async () => {
      const token = createMockJwt({ exp: Math.floor(Date.now() / 1000) + 7 * 24 * 3600 });
      (storage.getToken as jest.Mock).mockResolvedValue(token);

      const result = await validateStoredToken();
      expect(result.valid).toBe(true);
      expect(result.expiringSoon).toBe(false);
      expect(result.token).toBe(token);
    });

    it("flags expiringSoon when within 24h threshold", async () => {
      const token = createMockJwt({ exp: Math.floor(Date.now() / 1000) + 6 * 3600 }); // 6 hours left
      (storage.getToken as jest.Mock).mockResolvedValue(token);

      const result = await validateStoredToken();
      expect(result.valid).toBe(true);
      expect(result.expiringSoon).toBe(true);
    });
  });
});

describe("useAuthStore recording and expiration behavior", () => {
  beforeEach(() => {
    jest.clearAllMocks();
    useAuthStore.setState({
      isReady: true,
      isLoggedIn: true,
      userId: 1,
      isRecordingActive: false,
      sessionExpiredDuringRecording: false,
    });
  });

  it("clears credentials immediately on 401 when not recording", () => {
    useAuthStore.getState().handleUnauthorized();

    expect(storage.clearToken).toHaveBeenCalled();
    expect(storage.clearUserID).toHaveBeenCalled();
    expect(useAuthStore.getState().isLoggedIn).toBe(false);
    expect(useAuthStore.getState().sessionExpiredDuringRecording).toBe(false);
  });

  it("defers logout when 401 occurs during active recording", () => {
    useAuthStore.getState().setRecordingActive(true);
    useAuthStore.getState().handleUnauthorized();

    // Credentials should NOT be cleared yet
    expect(storage.clearToken).not.toHaveBeenCalled();
    expect(useAuthStore.getState().isLoggedIn).toBe(true);
    expect(useAuthStore.getState().sessionExpiredDuringRecording).toBe(true);

    // After workout finishes, finishDeferredUnauthorized clears credentials
    useAuthStore.getState().finishDeferredUnauthorized();
    expect(storage.clearToken).toHaveBeenCalled();
    expect(storage.clearUserID).toHaveBeenCalled();
    expect(useAuthStore.getState().isLoggedIn).toBe(false);
    expect(useAuthStore.getState().isRecordingActive).toBe(false);
    expect(useAuthStore.getState().sessionExpiredDuringRecording).toBe(false);
  });

  it("clears expired token on hydration", async () => {
    const expiredToken = createMockJwt({ exp: Math.floor(Date.now() / 1000) - 3600 });
    (storage.getToken as jest.Mock).mockResolvedValue(expiredToken);
    (storage.getUserID as jest.Mock).mockResolvedValue(1);

    await useAuthStore.getState().hydrate();

    expect(storage.clearToken).toHaveBeenCalled();
    expect(storage.clearUserID).toHaveBeenCalled();
    expect(useAuthStore.getState().isLoggedIn).toBe(false);
  });
});
