/**
 * PS-FTP request/response transport over react-native-ble-plx for the Polar H10.
 *
 * Transport behavior follows the Polar BLE SDK BlePsFtpClient (Polar SDK License):
 * frames are written to the PS-FTP MTU characteristic and the response arrives as
 * notifications on the same characteristic. One operation runs at a time.
 */
import { Buffer } from "buffer";
import type { Device, Subscription } from "react-native-ble-plx";

import {
  PSFTP_MTU_CHARACTERISTIC_UUID,
  PSFTP_SERVICE_UUID,
  RFC76_CANCEL_PACKET,
  Rfc76ResponseAssembler,
  buildQueryMessage,
  buildRequestMessage,
  buildRfc76Frames,
} from "./psftpProtocol";

export class PsftpTimeoutError extends Error {
  constructor(label: string, timeoutMs: number) {
    super(`PSFTP ${label} timed out after ${timeoutMs}ms`);
    this.name = "PsftpTimeoutError";
  }
}

export type PsftpWriteMode = "without_response" | "with_response";

interface PendingExchange {
  assembler: Rfc76ResponseAssembler;
  resolve: (payload: Uint8Array) => void;
  reject: (error: unknown) => void;
  timer: ReturnType<typeof setTimeout>;
}

export class PsftpClient {
  private subscription: Subscription | null = null;
  private pending: PendingExchange | null = null;
  private chain: Promise<unknown> = Promise.resolve();
  private closed = false;
  private mode: PsftpWriteMode = "without_response";

  constructor(
    private readonly device: Device,
    readonly packetSize: number = PsftpClient.packetSizeFor(device),
  ) {}

  /** ATT payload size (MTU - 3), never below the BLE default of 20 bytes. */
  static packetSizeFor(device: Device): number {
    const mtu = typeof device.mtu === "number" && device.mtu > 23 ? device.mtu : 23;
    return Math.max(20, mtu - 3);
  }

  get writeMode(): PsftpWriteMode {
    return this.mode;
  }

  open(): void {
    if (this.subscription || this.closed) return;
    this.subscription = this.device.monitorCharacteristicForService(
      PSFTP_SERVICE_UUID,
      PSFTP_MTU_CHARACTERISTIC_UUID,
      (error, characteristic) => {
        if (this.closed) return;
        if (error) {
          // ble-plx ends the monitor after an error; later exchanges must fail fast.
          this.subscription = null;
          this.failPending(this.pending, error);
          return;
        }
        if (!characteristic?.value) return;
        this.onPacket(Uint8Array.from(Buffer.from(characteristic.value, "base64")));
      },
    );
  }

  close(): void {
    if (this.closed) return;
    this.closed = true;
    this.failPending(this.pending, new Error("PSFTP client closed"));
    try {
      this.subscription?.remove();
    } catch {
      // Removing a monitor on a dropped connection can throw; nothing to clean up.
    }
    this.subscription = null;
  }

  query(id: number, params?: Uint8Array, timeoutMs = 10_000): Promise<Uint8Array> {
    return this.enqueue(buildQueryMessage(id, params), timeoutMs, `query ${id}`);
  }

  request(header: Uint8Array, timeoutMs = 30_000, label = "request"): Promise<Uint8Array> {
    return this.enqueue(buildRequestMessage(header), timeoutMs, label);
  }

  private enqueue(message: Uint8Array, timeoutMs: number, label: string): Promise<Uint8Array> {
    const run = () => this.exchange(message, timeoutMs, label);
    const result = this.chain.then(run, run);
    this.chain = result.catch(() => undefined);
    return result;
  }

  private exchange(message: Uint8Array, timeoutMs: number, label: string): Promise<Uint8Array> {
    if (this.closed || !this.subscription) {
      return Promise.reject(new Error("PSFTP client is not open"));
    }
    return new Promise<Uint8Array>((resolve, reject) => {
      const exchange: PendingExchange = {
        assembler: new Rfc76ResponseAssembler(),
        resolve,
        reject,
        timer: setTimeout(() => {
          this.failPending(exchange, new PsftpTimeoutError(label, timeoutMs));
        }, timeoutMs),
      };
      this.pending = exchange;
      void this.writeFrames(buildRfc76Frames(message, this.packetSize)).catch((error) => {
        this.failPending(exchange, error);
      });
    });
  }

  private async writeFrames(frames: Uint8Array[]): Promise<void> {
    for (const frame of frames) {
      const value = Buffer.from(frame).toString("base64");
      if (this.mode === "without_response") {
        try {
          await this.device.writeCharacteristicWithoutResponseForService(
            PSFTP_SERVICE_UUID,
            PSFTP_MTU_CHARACTERISTIC_UUID,
            value,
          );
          continue;
        } catch (error) {
          // The SDK writes requests without response. If the characteristic rejects
          // that write type, fall back once and keep the mode for later frames.
          if (!(await this.device.isConnected().catch(() => false))) throw error;
          this.mode = "with_response";
        }
      }
      await this.device.writeCharacteristicWithResponseForService(
        PSFTP_SERVICE_UUID,
        PSFTP_MTU_CHARACTERISTIC_UUID,
        value,
      );
    }
  }

  private onPacket(packet: Uint8Array): void {
    const exchange = this.pending;
    if (!exchange) return;
    const progress = exchange.assembler.push(packet);
    if (!progress.done) return;
    clearTimeout(exchange.timer);
    this.pending = null;
    if ("error" in progress) {
      if (progress.cancel) {
        void this.device
          .writeCharacteristicWithResponseForService(
            PSFTP_SERVICE_UUID,
            PSFTP_MTU_CHARACTERISTIC_UUID,
            Buffer.from(RFC76_CANCEL_PACKET).toString("base64"),
          )
          .catch(() => {});
      }
      exchange.reject(progress.error);
      return;
    }
    exchange.resolve(progress.payload);
  }

  private failPending(exchange: PendingExchange | null, error: unknown): void {
    if (!exchange || this.pending !== exchange) return;
    clearTimeout(exchange.timer);
    this.pending = null;
    exchange.reject(error);
  }
}
