import { IconSymbol } from "@/components/ui/icon-symbol";
import { Fonts, TacticalTheme } from "@/constants/theme";
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

const T = TacticalTheme.light;

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

  // Unregistered & idle -> render a clean Tactical Bright connect chip
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
          <IconSymbol name="heart.fill" size={14} color={T.heartRose} />
          <Text style={styles.connectChipText}>{t("home.hrConnectChip")}</Text>
          <IconSymbol
            name="dot.radiowaves.left.and.right"
            size={14}
            color={T.inkMuted}
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
      <View style={styles.leftRow}>
        {status === "Scanning" || status === "Connecting" ? (
          <ActivityIndicator
            size="small"
            color={T.electricBlue}
            style={styles.spinner}
          />
        ) : (
          <View
            style={[
              styles.statusDotOuter,
              isLive ? styles.statusDotOuterLive : styles.statusDotOuterIdle,
            ]}
          >
            <View
              style={[
                styles.statusDotCore,
                {
                  backgroundColor: isLive ? T.statusFresh : T.statusWarning,
                },
              ]}
            />
          </View>
        )}

        <View style={styles.infoCol}>
          <Text style={styles.deviceName} numberOfLines={1}>
            {activeSensor?.name ?? t("home.hrCardTitle")}
          </Text>
          <View
            style={[
              styles.statusBadge,
              isLive ? styles.statusBadgeLive : styles.statusBadgeIdle,
            ]}
          >
            <Text
              style={[
                styles.statusText,
                isLive ? styles.statusTextLive : styles.statusTextIdle,
              ]}
              numberOfLines={1}
            >
              {statusLabel}
            </Text>
          </View>
        </View>
      </View>

      <View style={styles.metricsCol}>
        <View style={styles.bpmPill}>
          <IconSymbol
            name="heart.fill"
            size={14}
            color={isLive ? T.heartRose : T.inkMuted}
          />
          <Text style={[styles.bpmValue, !isLive && styles.bpmValueMuted]}>
            {bpm > 0 ? bpm : "--"}
          </Text>
          <Text style={styles.bpmUnit}>BPM</Text>
        </View>
        {batteryLevel !== null && (
          <View style={styles.batteryRow}>
            <IconSymbol
              name={batteryLevel <= 20 ? "battery.25" : "battery.100"}
              size={12}
              color={batteryLevel <= 20 ? T.statusExhausted : T.statusFresh}
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
    backgroundColor: T.surface,
    borderWidth: 1,
    borderColor: T.border,
    borderRadius: 14,
    paddingHorizontal: 12,
    paddingVertical: 8,
    shadowColor: "#0F172A",
    shadowOffset: { width: 0, height: 1 },
    shadowOpacity: 0.04,
    shadowRadius: 3,
    elevation: 1,
  },
  connectChipText: {
    color: T.inkSecondary,
    fontSize: 12,
    fontWeight: "700",
    flexShrink: 1,
  },
  card: {
    backgroundColor: T.surface,
    borderRadius: 14,
    paddingHorizontal: 14,
    paddingVertical: 10,
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    borderWidth: 1,
    borderColor: T.border,
    shadowColor: "#0F172A",
    shadowOffset: { width: 0, height: 1 },
    shadowOpacity: 0.04,
    shadowRadius: 4,
    elevation: 1,
  },
  cardLive: {
    borderColor: T.statusFreshBorder,
  },
  leftRow: {
    flex: 1,
    flexDirection: "row",
    alignItems: "center",
    marginRight: 10,
  },
  spinner: {
    marginRight: 10,
  },
  statusDotOuter: {
    width: 14,
    height: 14,
    borderRadius: 7,
    alignItems: "center",
    justifyContent: "center",
    marginRight: 10,
  },
  statusDotOuterLive: {
    backgroundColor: T.statusFreshBg,
  },
  statusDotOuterIdle: {
    backgroundColor: T.statusWarningBg,
  },
  statusDotCore: {
    width: 7,
    height: 7,
    borderRadius: 3.5,
  },
  infoCol: {
    flex: 1,
    gap: 3,
  },
  deviceName: {
    fontFamily: Fonts?.mono,
    color: T.ink,
    fontSize: 13,
    fontWeight: "800",
    flexShrink: 1,
  },
  statusBadge: {
    alignSelf: "flex-start",
    paddingHorizontal: 7,
    paddingVertical: 2,
    borderRadius: 6,
    borderWidth: 1,
  },
  statusBadgeLive: {
    backgroundColor: T.statusFreshBg,
    borderColor: T.statusFreshBorder,
  },
  statusBadgeIdle: {
    backgroundColor: T.surfaceMuted,
    borderColor: T.border,
  },
  statusText: {
    fontSize: 10,
    fontWeight: "700",
    flexShrink: 1,
  },
  statusTextLive: {
    color: T.statusFresh,
  },
  statusTextIdle: {
    color: T.inkMuted,
  },
  metricsCol: {
    alignItems: "flex-end",
    gap: 3,
  },
  bpmPill: {
    flexDirection: "row",
    alignItems: "center",
    gap: 5,
    backgroundColor: T.surfaceMuted,
    borderWidth: 1,
    borderColor: T.border,
    paddingHorizontal: 10,
    paddingVertical: 5,
    borderRadius: 10,
  },
  bpmValue: {
    fontFamily: Fonts?.mono,
    color: T.ink,
    fontSize: 16,
    fontWeight: "900",
    fontVariant: ["tabular-nums"],
  },
  bpmValueMuted: {
    color: T.inkMuted,
  },
  bpmUnit: {
    fontFamily: Fonts?.mono,
    color: T.inkMuted,
    fontSize: 10,
    fontWeight: "800",
  },
  batteryRow: {
    flexDirection: "row",
    alignItems: "center",
    gap: 3,
  },
  batteryText: {
    fontFamily: Fonts?.mono,
    color: T.inkMuted,
    fontSize: 10,
    fontWeight: "700",
    fontVariant: ["tabular-nums"],
  },
  batteryTextLow: {
    color: T.statusExhausted,
  },
});
