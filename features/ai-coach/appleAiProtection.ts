export interface AppleAiPowerSample {
  observedAt?: number;
  battery: number; // 0..1; -1 means unavailable
  lowPower: boolean | null;
  thermal: number; // ProcessInfo: nominal=0 ... critical=3; -1 unknown
}

export interface AppleAiProtection {
  batteryLimited: boolean;
  lowPowerLimited: boolean;
  thermalLimited: boolean;
  recoveringSince: number | null;
  reason: 'power' | 'thermal' | 'recovering' | null;
}

export const initialAppleAiProtection: AppleAiProtection = {
  batteryLimited: false, lowPowerLimited: false, thermalLimited: false,
  recoveringSince: null, reason: null,
};

export function advanceAppleAiProtection(
  previous: AppleAiProtection, sample: AppleAiPowerSample, now: number,
): AppleAiProtection {
  const validBattery = Number.isFinite(sample.battery) && sample.battery >= 0 && sample.battery <= 1;
  const batteryLimited = validBattery
    ? sample.battery <= 0.15 || (previous.batteryLimited && sample.battery < 0.2)
    : previous.batteryLimited;
  const lowPowerLimited = sample.lowPower ?? previous.lowPowerLimited;
  const thermalLimited = sample.thermal >= 0 ? sample.thermal >= 2 : previous.thermalLimited;
  let recoveringSince = previous.recoveringSince;
  let reason: AppleAiProtection['reason'] = null;
  if (thermalLimited || batteryLimited || lowPowerLimited) {
    reason = thermalLimited ? 'thermal' : 'power';
    recoveringSince = null;
  } else if (previous.reason) {
    recoveringSince ??= now;
    if (now - recoveringSince < 30_000) reason = 'recovering';
    else recoveringSince = null;
  }
  return { batteryLimited, lowPowerLimited, thermalLimited, recoveringSince, reason };
}
