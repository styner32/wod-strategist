import { act, renderHook } from '@testing-library/react-native';
import { Buffer } from 'buffer';
import { setRegisteredBleDevice, useBleHeartRate } from '../useBleHeartRate';
import { HR_CHARACTERISTIC_UUID } from '../polar/polarPmdProtocol';
let mockState: (state: string) => void;
let mockScan: (error: unknown, device: unknown) => void;
let mockHR: (error: unknown, characteristic?: { value: string }) => void;
let mockLost: (error: unknown) => void;
jest.mock('react-native-ble-plx', () => ({
 State: { PoweredOn: 'PoweredOn' },
 BleManager: jest.fn().mockImplementation(() => ({
  onStateChange: (cb: typeof mockState) => { mockState=cb; return { remove: jest.fn() }; },
  startDeviceScan: (_filter: unknown, _opts: unknown, cb: typeof mockScan) => {mockScan=cb;},
  stopDeviceScan: jest.fn(),
 })),
}));
describe('BLE accepted and raw HR paths', () => {
 beforeEach(async () => {
  jest.useFakeTimers();
  jest.setSystemTime(10000);
  await setRegisteredBleDevice(null);
 });
 afterEach(() => {jest.useRealTimers();});
 const device = {
  name: 'Polar H10', id: 'test',
  connect: async () => device,
  discoverAllServicesAndCharacteristics: async () => device,
  services: async () => [],
  readCharacteristicForService: async () => null,
  cancelConnection: jest.fn(),
  monitorCharacteristicForService: (_service: string, characteristic: string, cb: typeof mockHR) => {
   if (characteristic === HR_CHARACTERISTIC_UUID) mockHR=cb;
   return { remove: jest.fn() };
  },
  onDisconnected: (cb: typeof mockLost) => {mockLost=cb;return {remove:jest.fn()};},
 };
 const receive = (bpm: number, flags=6, advance=1000) => act(() => {
  jest.advanceTimersByTime(advance);
  mockHR(null,{value:Buffer.from([flags,bpm]).toString('base64')});
 });
 it('forwards false contact raw, hides bad BPM, recovers and expires after exactly five seconds', async () => {
  const sink={onHeartRate:jest.fn(),onDeviceReady:jest.fn(),onDeviceLost:jest.fn()};
  const onReading=jest.fn();
  const view=renderHook(() => useBleHeartRate({sink,onReading}));
  await act(async () => {mockState('PoweredOn');mockScan(null,device);});
  receive(150);receive(150);receive(150);receive(45,4);
  expect(sink.onHeartRate).toHaveBeenLastCalledWith(45,[],14000,false);
  expect(view.result.current.bpm).toBe(0);expect(view.result.current.quality).toBe('contact_loss');
  expect(onReading).toHaveBeenLastCalledWith(expect.objectContaining({bpm:null}));
  for(let i=0;i<5;i++) receive(150);
  expect(view.result.current.bpm).toBe(0);
  receive(150);expect(view.result.current.bpm).toBe(150);
  act(()=>jest.advanceTimersByTime(5000));
  expect(view.result.current.getReading().bpm).toBeNull();expect(view.result.current.bpm).toBe(0);
  act(()=>mockHR(null,{value:""}));
  expect(view.result.current.quality).toBe("invalid");
  view.unmount();
 });
 it('clears values on disconnect and starts a new session with no prior drop baseline',async()=>{
  const view=renderHook<ReturnType<typeof useBleHeartRate>, {recording:boolean}>(({recording})=>useBleHeartRate({recording}),{initialProps:{recording:false}});
  await act(async()=>{mockState('PoweredOn');mockScan(null,device);});
  receive(150);receive(150);receive(150);receive(45);
  expect(view.result.current.quality).toBe('sudden_drop');
  view.rerender({recording:true});receive(45,0);
  expect(view.result.current.quality).toBe('low');expect(view.result.current.bpm).toBe(45);
  act(()=>mockLost(null));expect(view.result.current.bpm).toBe(0);
  view.unmount();
 });
 it('ignores non-registered sensors during scan and switches connection when registering a target sensor', async () => {
  const otherSensor: any = {
   ...device,
   id: 'sense-id',
   name: 'Polar Sense 0DCADB36',
   connect: jest.fn(async () => otherSensor),
   cancelConnection: jest.fn(),
  };
  const myH10: any = {
   ...device,
   id: 'h10-id',
   name: 'Polar H10 99887766',
   connect: jest.fn(async () => myH10),
   cancelConnection: jest.fn(),
  };

  await setRegisteredBleDevice({ id: 'h10-id', name: 'Polar H10 99887766' });
  const sink = { onHeartRate: jest.fn(), onDeviceReady: jest.fn(), onDeviceLost: jest.fn() };
  const view = renderHook(() => useBleHeartRate({ sink }));

  await act(async () => {
   mockState('PoweredOn');
   mockScan(null, otherSensor);
  });

  // Other person's Polar Sense is discovered for the UI list, but not auto-connected
  expect(otherSensor.connect).not.toHaveBeenCalled();
  expect(view.result.current.connectedDevice).toBeNull();
  expect(view.result.current.discoveredDevices).toEqual([
   { id: 'sense-id', name: 'Polar Sense 0DCADB36' },
  ]);

  // Scanning our registered H10 connects immediately
  await act(async () => {
   mockScan(null, myH10);
  });
  expect(myH10.connect).toHaveBeenCalledTimes(1);
  expect(view.result.current.connectedDevice).toEqual({
   id: 'h10-id',
   name: 'Polar H10 99887766',
  });

  // Switching registered device to the discovered otherSensor disconnects myH10 and connects otherSensor
  await act(async () => {
   await view.result.current.registerDevice({ id: 'sense-id', name: 'Polar Sense 0DCADB36' });
  });
  expect(myH10.cancelConnection).toHaveBeenCalledTimes(1);
  expect(otherSensor.connect).toHaveBeenCalledTimes(1);
  expect(view.result.current.connectedDevice).toEqual({
   id: 'sense-id',
   name: 'Polar Sense 0DCADB36',
  });
  view.unmount();
 });
});
