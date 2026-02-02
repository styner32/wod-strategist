import React, { useEffect, useState } from "react";
import {
  ActivityIndicator,
  FlatList,
  RefreshControl,
  StyleSheet,
  Text,
  View,
} from "react-native";
import { AnalysisResult, fetchAnalysisHistory } from "../history";

export function HistoryList() {
  const [data, setData] = useState<AnalysisResult[]>([]);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);

  const loadData = async () => {
    try {
      const history = await fetchAnalysisHistory();
      setData(history);
    } catch (e) {
      console.error(e);
    } finally {
      setLoading(false);
      setRefreshing(false);
    }
  };

  useEffect(() => {
    loadData();
  }, []);

  const onRefresh = () => {
    setRefreshing(true);
    loadData();
  };

  const renderItem = ({ item }: { item: AnalysisResult }) => (
    <View style={styles.card}>
      <View style={styles.header}>
        <Text style={styles.sessionId}>{item.session_id}</Text>
        <Text style={[styles.status, item.status === "COMPLETED" ? styles.success : styles.pending]}>
          {item.status}
        </Text>
      </View>
      <Text style={styles.date}>{new Date(item.created_at).toLocaleString()}</Text>
      <Text style={styles.output} numberOfLines={3}>
        {item.output}
      </Text>
    </View>
  );

  if (loading) {
    return <ActivityIndicator style={{ flex: 1 }} color="#000" />;
  }

  return (
    <FlatList
      data={data}
      keyExtractor={(item) => item.id.toString()}
      renderItem={renderItem}
      refreshControl={
        <RefreshControl refreshing={refreshing} onRefresh={onRefresh} />
      }
      contentContainerStyle={styles.list}
      ListEmptyComponent={<Text style={styles.empty}>No workout history found.</Text>}
    />
  );
}

const styles = StyleSheet.create({
  list: {
    padding: 16,
  },
  card: {
    backgroundColor: "#fff",
    padding: 16,
    borderRadius: 8,
    marginBottom: 12,
    shadowColor: "#000",
    shadowOpacity: 0.1,
    shadowOffset: { width: 0, height: 2 },
    shadowRadius: 4,
    elevation: 2,
  },
  header: {
    flexDirection: "row",
    justifyContent: "space-between",
    alignItems: "center",
    marginBottom: 4,
  },
  sessionId: {
    fontSize: 16,
    fontWeight: "bold",
  },
  status: {
    fontSize: 12,
    fontWeight: "bold",
  },
  success: { color: "green" },
  pending: { color: "orange" },
  date: {
    fontSize: 12,
    color: "#666",
    marginBottom: 8,
  },
  output: {
    fontSize: 14,
    color: "#333",
  },
  empty: {
    textAlign: "center",
    marginTop: 40,
    color: "#888",
  },
});
