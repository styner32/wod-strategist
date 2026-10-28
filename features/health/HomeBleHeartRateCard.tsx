import { IconSymbol } from "@/components/ui/icon-symbol";
import { t, useLocale } from "@/features/i18n";
import { useFocusEffect } from "expo-router";
import React, { useCallback, useEffect, useState } from "react";
import {
  ActivityIndicator,
  Alert,
  AppState,
  StyleSheet,
  Text,
  TouchableOpacity,
  View,
} from "react-native";
import {
  getRegisteredBleDevice,
  type RegisteredBleDevice,
  useBleHeartRate,
} from "./useBleHeartRate";

function isForegroundAppState(state: string | null | undefined): boolean {
  return state !== "background" && state !== "inactive";
}

export function HomeBleHeartRateCard() {
  useLocale();
  const [isFocused, setIsFocused] = useState(false);
  const [isAppActive, setIsAppActive] = useState(() =>
    isForegroundAppState(AppState.currentState),
  );
  const [storedRegistration, setStoredRegistration] =
    useState<RegisteredBleDevice | null>(null);
  const [manualScanActive, setManualScanActive] = useState(false);

  useFocusEffect(
    useCallback(() => {
      setIsFocused(true);
      void getRegisteredBleDevice().then(setStoredRegistration);
      return () => {
        setIsFocused(false);
        setManualScanActive(false);
      };
    }, []),
  );

  useEffect(() => {
    const sub = AppState.addEventListener("change", (nextState) => {
      setIsAppActive(isForegroundAppState(nextState));
    });
    return () => sub.remove();
  }, []);

  const hasRegisteredTarget = Boolean(storedRegistration);
  const bleEnabled =
    isFocused && isAppActive && (hasRegisteredTarget || manualScanActive);

  const {
    bpm,
    status,
    batteryLevel,
    connectedDevice,
    registeredDevice,
    discoveredDevices,
    registerDevice,
    rescanDevices,
  } = useBleHeartRate({
    enabled: bleEnabled,
  });

  const effectiveRegistration = registeredDevice ?? storedRegistration;
  const activeSensor = connectedDevice ?? effectiveRegistration;

  const handleOpenPicker = useCallback(() => {
    const lines = [
      connectedDevice
        ? t("overlay.recording.hrDeviceConnected", {
            name: connectedDevice.name,
          })
        : t("overlay.recording.hrDeviceDisconnected"),
      effectiveRegistration
        ? t("overlay.recording.hrDeviceRegisteredInfo", {
            name: effectiveRegistration.name,
          })
        : t("overlay.recording.hrDeviceUnregisteredInfo"),
    ];

    if (
      !connectedDevice &&
      discoveredDevices.length === 0 &&
      status === "Scanning"
    ) {
      lines.push(t("home.hrNoDevicesFound"));
    }

    const buttons: {
      text: string;
      style?: "default" | "cancel" | "destructive";
      onPress?: () => void;
    }[] = [];

    if (
      !connectedDevice ||
      !effectiveRegistration ||
      effectiveRegistration.id !== connectedDevice.id
    ) {
      if (connectedDevice) {
        buttons.push({
          text: t("overlay.recording.hrDeviceRegisterCurrent", {
            name: connectedDevice.name,
          }),
          onPress: () => {
            setStoredRegistration(connectedDevice);
            void registerDevice(connectedDevice);
            setManualScanActive(false);
          },
        });
      }
    }

    for (const dev of discoveredDevices) {
      if (connectedDevice?.id === dev.id) continue;
      buttons.push({
        text: t("overlay.recording.hrDeviceSwitchTo", { name: dev.name }),
        onPress: () => {
          setStoredRegistration(dev);
          void registerDevice(dev);
          setManualScanActive(false);
        },
      });
    }

    if (effectiveRegistration) {
      buttons.push({
        text: t("overlay.recording.hrDeviceUnregister"),
        style: "destructive",
        onPress: () => {
          setStoredRegistration(null);
          setManualScanActive(false);
          void registerDevice(null);
        },
      });
    } else if (manualScanActive) {
      buttons.push({
        text: t("home.hrStopScan"),
        style: "destructive",
        onPress: () => {
          setManualScanActive(false);
        },
      });
    }

    buttons.push({
      text: t("overlay.recording.hrDeviceRescan"),
      onPress: () => {
        setManualScanActive(true);
        void rescanDevices();
      },
    });

    buttons.push({
      text: t("common.cancel"),
      style: "cancel",
    });

    Alert.alert(
      t("overlay.recording.hrDeviceTitle"),
      lines.join("\n"),
      buttons,
    );
  }, [
    connectedDevice,
    discoveredDevices,
    effectiveRegistration,
    manualScanActive,
    registerDevice,
    rescanDevices,
    status,
  ]);

  // Unregistered & idle -> render a small, non-intrusive connect chip
  if (!activeSensor && !manualScanActive) {
    return (
      <View style={styles.chipRow}>
        <TouchableOpacity
          testID="home-ble-connect-chip"
          style={styles.connectChip}
          onPress={() => setManualScanActive(true)}
          activeOpacity={0.75}
          accessibilityRole="button"
        >
          <IconSymbol name="heart.fill" size={14} color="#FF375F" />
          <Text style={styles.connectChipText}>{t("home.hrConnectChip")}</Text>
          <IconSymbol
            name="dot.radiowaves.left.and.right"
            size={14}
            color="#64748B"
          />
        </TouchableOpacity>
      </View>
    );
  }

  const isLive = status === "Live" && Boolean(connectedDevice);
  const statusLabel = isLive
    ? t("home.hrStatusLive")
    : status === "Connecting"
      ? t("home.hrStatusConnecting")
      : status === "Error"
        ? t("home.hrStatusError")
        : activeSensor
          ? t("home.hrStatusScanning")
          : t("home.hrScanningChip");

  return (
    <TouchableOpacity
      testID="home-ble-hr-card"
      style={[styles.card, isLive && styles.cardLive]}
      onPress={handleOpenPicker}
      activeOpacity={0.8}
      accessibilityRole="button"
      accessibilityLabel={t("home.hrCardTitle")}
    >
      <View
        style={[
          styles.iconBox,
          isLive ? styles.iconBoxLive : styles.iconBoxIdle,
        ]}
      >
        <IconSymbol
          name="heart.fill"
          size={20}
          color={isLive ? "#FF375F" : "#94A3B8"}
        />
      </View>

      <View style={styles.infoCol}>
        <Text style={styles.deviceName} numberOfLines={1}>
          {activeSensor?.name ?? t("home.hrCardTitle")}
        </Text>
        <View style={styles.statusRow}>
          {status === "Scanning" || status === "Connecting" ? (
            <ActivityIndicator
              size="small"
              color="#64D2FF"
              style={styles.spinner}
            />
          ) : (
            <View
              style={[
                styles.statusDot,
                { backgroundColor: isLive ? "#30D158" : "#FF9F0A" },
              ]}
            />
          )}
          <Text style={styles.statusText} numberOfLines={1}>
            {statusLabel}
          </Text>
        </View>
      </View>

      <View style={styles.metricsCol}>
        <View style={styles.bpmRow}>
          <Text style={[styles.bpmValue, !isLive && styles.bpmValueMuted]}>
            {bpm > 0 ? bpm : "--"}
          </Text>
          <Text style={styles.bpmUnit}>BPM</Text>
        </View>
        {batteryLevel !== null && (
          <View style={styles.batteryRow}>
            <IconSymbol
              name={batteryLevel <= 20 ? "battery.25" : "battery.100"}
              size={13}
              color={batteryLevel <= 20 ? "#FF453A" : "#30D158"}
            />
            <Text
              style={[
                styles.batteryText,
                batteryLevel <= 20 && styles.batteryTextLow,
              ]}
            >
              {batteryLevel}%
            </Text>
          </View>
        )}
      </View>
    </TouchableOpacity>
  );
}

const styles = StyleSheet.create({
  chipRow: {
    flexDirection: "row",
    alignItems: "center",
  },
  connectChip: {
    flexDirection: "row",
    alignItems: "center",
    gap: 6,
    backgroundColor: "#121720",
    borderWidth: 1,
    borderColor: "#1E2633",
    borderRadius: 20,
    paddingHorizontal: 12,
    paddingVertical: 8,
  },
  connectChipText: {
    color: "#CBD5E1",
    fontSize: 12,
    fontWeight: "600",
  },
  card: {
    backgroundColor: "#121720",
    borderRadius: 16,
    padding: 14,
    flexDirection: "row",
    alignItems: "center",
    borderWidth: 1,
    borderColor: "#1E2633",
  },
  cardLive: {
    borderColor: "rgba(255, 55, 95, 0.4)",
  },
  iconBox: {
    width: 42,
    height: 42,
    borderRadius: 12,
    justifyContent: "center",
    alignItems: "center",
    marginRight: 12,
  },
  iconBoxLive: {
    backgroundColor: "rgba(255, 55, 95, 0.16)",
  },
  iconBoxIdle: {
    backgroundColor: "#1A2230",
  },
  infoCol: {
    flex: 1,
    marginRight: 10,
  },
  deviceName: {
    color: "#F8FAFC",
    fontSize: 14,
    fontWeight: "700",
  },
  statusRow: {
    flexDirection: "row",
    alignItems: "center",
    marginTop: 3,
    gap: 5,
  },
  spinner: {
    transform: [{ scale: 0.7 }],
  },
  statusDot: {
    width: 6,
    height: 6,
    borderRadius: 3,
  },
  statusText: {
    color: "#94A3B8",
    fontSize: 12,
    flexShrink: 1,
  },
  metricsCol: {
    alignItems: "flex-end",
  },
  bpmRow: {
    flexDirection: "row",
    alignItems: "baseline",
    gap: 3,
  },
  bpmValue: {
    color: "#FFFFFF",
    fontSize: 20,
    fontWeight: "800",
    fontVariant: ["tabular-nums"],
  },
  bpmValueMuted: {
    color: "#64748B",
  },
  bpmUnit: {
    color: "#94A3B8",
    fontSize: 11,
    fontWeight: "700",
  },
  batteryRow: {
    flexDirection: "row",
    alignItems: "center",
    gap: 3,
    marginTop: 2,
  },
  batteryText: {
    color: "#94A3B8",
    fontSize: 11,
    fontWeight: "600",
  },
  batteryTextLow: {
    color: "#FF453A",
  },
});
