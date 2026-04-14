import { StyleSheet } from "react-native";

import ParallaxScrollView from "@/components/parallax-scroll-view";
import { ThemedText } from "@/components/themed-text";
import { ThemedView } from "@/components/themed-view";
import { IconSymbol } from "@/components/ui/icon-symbol";
import { t } from "@/features/i18n";
import { HistoryList, useHistoryData } from "@/features/wod/ui/HistoryList";

export default function HistoryScreen() {
  const { data, loading, refreshing, onRefresh } = useHistoryData();

  return (
    <ParallaxScrollView
      headerBackgroundColor={{ light: "#D0D0D0", dark: "#353636" }}
      headerImage={
        <IconSymbol
          size={310}
          color="#808080"
          name="figure.run"
          style={styles.headerImage}
        />
      }
      refreshing={refreshing}
      onRefresh={onRefresh}
    >
      <ThemedView style={styles.titleContainer}>
        <ThemedText type="title">{t("history.title")}</ThemedText>
      </ThemedView>
      <ThemedText>{t("history.subtitle")}</ThemedText>

      <HistoryList data={data} loading={loading} />
    </ParallaxScrollView>
  );
}

const styles = StyleSheet.create({
  headerImage: {
    color: "#808080",
    bottom: -60,
    left: 0,
    position: "absolute",
  },
  titleContainer: {
    flexDirection: "row",
    gap: 8,
  },
  backgroundColor: {
    backgroundColor: "#1A1A1A",
  },
});
