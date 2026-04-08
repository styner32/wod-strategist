import { IconSymbol } from "@/components/ui/icon-symbol";
import { Link } from "expo-router";
import { Pressable, ScrollView, StyleSheet, Text, View } from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";

export default function TrainScreen() {
  return (
    <SafeAreaView style={styles.container} edges={["left", "right"]}>
      <ScrollView
        contentContainerStyle={styles.scrollContent}
        showsVerticalScrollIndicator={false}
      >
        {/* Hero Section */}
        <View style={styles.hero}>
          <Text style={styles.heroTitle}>Ready to Train?</Text>
          <Text style={styles.heroSubtitle}>
            Execute your session with real-time biometric tracking and kinetic
            analysis.
          </Text>
        </View>

        {/* Primary Actions */}
        <View style={styles.section}>
          <Text style={styles.sectionTitle}>Start Session</Text>

          <Link href="/workout/setup" asChild>
            <Pressable style={styles.primaryCard}>
              <View style={styles.primaryIconBox}>
                <IconSymbol name="flame.fill" size={28} color="#00E5FF" />
              </View>
              <View style={{ flex: 1 }}>
                <Text style={styles.primaryCardTitle}>Start Workout</Text>
                <Text style={styles.primaryCardDesc}>
                  Real-time pose estimation, rep counting & form correction
                </Text>
              </View>
              <Text style={styles.chevron}>›</Text>
            </Pressable>
          </Link>

          <Link href="/upload" asChild>
            <Pressable style={styles.card}>
              <View style={styles.iconBox}>
                <IconSymbol
                  name="arrow.up.circle.fill"
                  size={22}
                  color="#53E16F"
                />
              </View>
              <View style={{ flex: 1 }}>
                <Text style={styles.cardTitle}>Analyse Form</Text>
                <Text style={styles.cardDesc}>
                  Upload footage for AI biomechanical breakdown
                </Text>
              </View>
              <Text style={styles.chevron}>›</Text>
            </Pressable>
          </Link>
        </View>

        {/* Queue Section */}
        <View style={styles.section}>
          <Text style={styles.sectionTitle}>Processing</Text>

          <Link href={"/queue" as any} asChild>
            <Pressable style={styles.card}>
              <View style={styles.iconBox}>
                <IconSymbol name="arrow.clockwise" size={22} color="#FFD60A" />
              </View>
              <View style={{ flex: 1 }}>
                <Text style={styles.cardTitle}>Video Queue</Text>
                <Text style={styles.cardDesc}>
                  Encoding & upload status
                </Text>
              </View>
              <Text style={styles.chevron}>›</Text>
            </Pressable>
          </Link>
        </View>
      </ScrollView>
    </SafeAreaView>
  );
}

const styles = StyleSheet.create({
  container: { flex: 1, backgroundColor: "#000" },
  scrollContent: { padding: 20, paddingBottom: 40 },

  hero: {
    marginBottom: 32,
    paddingTop: 8,
  },
  heroTitle: {
    color: "#fff",
    fontSize: 32,
    fontWeight: "800",
    letterSpacing: -0.5,
  },
  heroSubtitle: {
    color: "#666",
    fontSize: 15,
    marginTop: 8,
    lineHeight: 22,
  },

  section: { marginBottom: 28 },
  sectionTitle: {
    color: "#888",
    fontSize: 12,
    fontWeight: "700",
    textTransform: "uppercase",
    letterSpacing: 1.2,
    marginBottom: 14,
  },

  primaryCard: {
    backgroundColor: "#1A1A1A",
    padding: 20,
    borderRadius: 16,
    flexDirection: "row",
    alignItems: "center",
    marginBottom: 12,
  },
  primaryIconBox: {
    width: 56,
    height: 56,
    backgroundColor: "#002B3D",
    borderRadius: 16,
    justifyContent: "center",
    alignItems: "center",
    marginRight: 16,
  },
  primaryCardTitle: {
    color: "#fff",
    fontSize: 18,
    fontWeight: "700",
    marginBottom: 4,
  },
  primaryCardDesc: {
    color: "#888",
    fontSize: 13,
    lineHeight: 18,
  },

  card: {
    backgroundColor: "#1A1A1A",
    padding: 16,
    borderRadius: 16,
    flexDirection: "row",
    alignItems: "center",
    marginBottom: 12,
  },
  iconBox: {
    width: 44,
    height: 44,
    backgroundColor: "#252525",
    borderRadius: 12,
    justifyContent: "center",
    alignItems: "center",
    marginRight: 14,
  },
  cardTitle: { color: "#fff", fontSize: 16, fontWeight: "600" },
  cardDesc: { color: "#666", fontSize: 13, marginTop: 2 },
  chevron: { color: "#444", fontSize: 24, fontWeight: "300" },
});
