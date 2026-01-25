// src/app/index.tsx
import { Link } from "expo-router";
import { Pressable, StyleSheet, Text, View } from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";

export default function Dashboard() {
  return (
    <SafeAreaView style={styles.container}>
      <View style={styles.header}>
        <Text style={styles.title}>Welcome Coach,</Text>
        <Text style={styles.subtitle}>Ready to verify AI Engine?</Text>
      </View>

      <View style={styles.menu}>
        <Text style={styles.sectionTitle}>Development Zone</Text>

        <Link href="/workout/visionTestPage" asChild>
          <Pressable style={styles.card}>
            <View style={styles.iconBox}>
              <Text style={styles.icon}>👁️</Text>
            </View>
            <View>
              <Text style={styles.cardTitle}>Vision AI Test</Text>
              <Text style={styles.cardDesc}>Camera & Skeleton Check</Text>
            </View>
          </Pressable>
        </Link>
      </View>
    </SafeAreaView>
  );
}

const styles = StyleSheet.create({
  container: { flex: 1, backgroundColor: "#000", padding: 20 },
  header: { marginBottom: 40, marginTop: 20 },
  title: { color: "#fff", fontSize: 28, fontWeight: "800" },
  subtitle: { color: "#666", fontSize: 16, marginTop: 5 },
  sectionTitle: {
    color: "#888",
    fontSize: 14,
    marginBottom: 15,
    textTransform: "uppercase",
    letterSpacing: 1,
  },
  menu: { gap: 15 },
  card: {
    backgroundColor: "#1A1A1A",
    padding: 20,
    borderRadius: 16,
    flexDirection: "row",
    alignItems: "center",
    borderWidth: 1,
    borderColor: "#333",
  },
  iconBox: {
    width: 50,
    height: 50,
    backgroundColor: "#333",
    borderRadius: 12,
    justifyContent: "center",
    alignItems: "center",
    marginRight: 15,
  },
  icon: { fontSize: 24 },
  cardTitle: { color: "#fff", fontSize: 18, fontWeight: "bold" },
  cardDesc: { color: "#888", fontSize: 14, marginTop: 2 },
});
