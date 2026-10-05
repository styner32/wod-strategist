import * as Battery from 'expo-battery';
import { appleOnDeviceAi } from '../../modules/apple-on-device-ai';
import type { AppleAiPowerSample } from './appleAiProtection';

let sampledAt = -Infinity;
let cached: Promise<AppleAiPowerSample> | null = null;
/** Both observers share a single five-second power query. No timer of its own. */
export function readAppleAiPower(): Promise<AppleAiPowerSample> {
  const now = Date.now();
  if (cached && now >= sampledAt && now - sampledAt < 4500) return cached;
  sampledAt = now;
  cached = Promise.allSettled([Battery.getPowerStateAsync(), appleOnDeviceAi.getThermalState()]).then(([power, thermal]) => ({
    observedAt: now,
    battery: power.status === 'fulfilled' ? power.value.batteryLevel : -1,
    lowPower: power.status === 'fulfilled' ? power.value.lowPowerMode : null,
    thermal: thermal.status === 'fulfilled' ? thermal.value : -1,
  }));
  return cached;
}
