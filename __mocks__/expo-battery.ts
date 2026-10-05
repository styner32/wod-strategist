export const getBatteryLevelAsync = jest.fn().mockResolvedValue(0.84);
export const addBatteryLevelListener = jest.fn().mockReturnValue({ remove: jest.fn() });
export const isAvailableAsync = jest.fn().mockResolvedValue(true);
