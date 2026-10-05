import { jest, it, expect, beforeEach } from '@jest/globals';
import { api } from '../../api/client';
import { readEnvironmentRecords } from '../../api/onDeviceAi';
jest.mock('../../api/client', () => ({api:{get:jest.fn()}}));
const first='environment_01ARZ3NDEKTSV4RRFFQ69G5FAV.json';
const second='environment_01ARZ3NDEKTSV4RRFFQ69G5FAW.json';
beforeEach(() => { jest.clearAllMocks(); });
it('loads review/comparison JSON across pages only on request, without reading images or old posture files', async () => {
  expect(api.get).not.toHaveBeenCalled();
  jest.mocked(api.get).mockImplementation(async (path:string) => {
    if (path.includes('/asset?')) return {id:path.includes(first)?'original':'review'} as any;
    if (path.includes('after=next')) return {files:[second,first],next_cursor:''} as any;
    return {files:[first,'apple_ai_a.json','environment_01ARZ3NDEKTSV4RRFFQ69G5FAV_0.jpg','environment_01ARZ3NDEKTSV4RRFFQ69G5FAV_events.ndjson'],next_cursor:'next'} as any;
  });
  expect(await readEnvironmentRecords('session',7)).toEqual([{id:'original'},{id:'review'}]);
  expect(api.get).toHaveBeenCalledTimes(4);
  expect(jest.mocked(api.get).mock.calls.every(([path]) => path.includes('profile_id=7'))).toBe(true);
});
it('does not report an incomplete scan as a complete set of reviews', async () => {
  jest.mocked(api.get).mockImplementation(async (path:string) => {
    if (path.includes('/asset?')) throw new Error('upload incomplete');
    return {files:[first],next_cursor:''} as any;
  });
  await expect(readEnvironmentRecords('session',7)).rejects.toThrow('upload incomplete');
});
it('rejects a repeated pagination cursor instead of looping forever', async () => {
  jest.mocked(api.get).mockResolvedValue({files:[],next_cursor:'same'});
  await expect(readEnvironmentRecords('session',7)).rejects.toThrow('Repeated archive cursor');
  expect(api.get).toHaveBeenCalledTimes(2);
});
