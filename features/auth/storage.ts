import * as SecureStore from "expo-secure-store";

const TOKEN_KEY = "auth_token";
const USER_ID_KEY = "auth_user_id";

export const getToken = () => SecureStore.getItemAsync(TOKEN_KEY);
export const setToken = (t: string) => SecureStore.setItemAsync(TOKEN_KEY, t);
export const clearToken = () => SecureStore.deleteItemAsync(TOKEN_KEY);

export const getUserID = () => SecureStore.getItemAsync(USER_ID_KEY);
export const setUserID = (id: string) =>
  SecureStore.setItemAsync(USER_ID_KEY, id);
export const clearUserID = () => SecureStore.deleteItemAsync(USER_ID_KEY);
