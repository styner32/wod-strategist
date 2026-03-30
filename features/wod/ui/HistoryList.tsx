import React, { useEffect, useState } from "react";
import {
  ActivityIndicator,
  FlatList,
  RefreshControl,
  StyleSheet,
  Text,
  TouchableOpacity,
  View,
} from "react-native";
import { AnalysisResult, fetchAnalysisHistory } from "../history";
import { useProfileId } from "@/store/useProfileStore";

export function HistoryList() {
  const profileId = useProfileId();
  const [data, setData] = useState<AnalysisResult[]>([]);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [expandedId, setExpandedId] = useState<number | null>(null);

  const loadData = async () => {
    if (!profileId) {
      setData([]);
      setLoading(false);
      setRefreshing(false);
      return;
    }
    try {
      const history = await fetchAnalysisHistory(profileId);
      setData(history);
    } catch (e) {
      console.error(e);
    } finally {
      setLoading(false);
      setRefreshing(false);
    }
  };

  useEffect(() => {
    setLoading(true);
    loadData();
  }, [profileId]);

  const onRefresh = () => {
    setRefreshing(true);
    loadData();
  };

  const toggleExpand = (id: number) => {
    setExpandedId(prev => (prev === id ? null : id));
  };

  const renderItem = ({ item }: { item: AnalysisResult }) => {
    const isExpanded = expandedId === item.id;
    return (
      <TouchableOpacity 
        style={styles.card} 
        onPress={() => toggleExpand(item.id)}
        activeOpacity={0.7}
      >
        <View style={styles.header}>
          <Text style={styles.sessionId}>{item.session_id}</Text>
          <Text style={[styles.status, item.status === "COMPLETED" ? styles.success : styles.pending]}>
            {item.status}
          </Text>
        </View>
        <Text style={styles.date}>{new Date(item.created_at).toLocaleString()}</Text>
        <Text style={styles.output} numberOfLines={isExpanded ? undefined : 3}>
          {item.output}
        </Text>
        <Text style={styles.hint}>
          {isExpanded ? "Show Less" : "Show More"}
        </Text>
      </TouchableOpacity>
    );
  };

  if (loading && !refreshing) {
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
      scrollEnabled={false} // Disable internal scrolling to avoid conflict with parent ScrollView
    />
  );
}

const styles = StyleSheet.create({
  list: {
    padding: 16,
    paddingBottom: 40,
  },
  refreshBtn: {
    alignSelf: 'flex-end',
    marginBottom: 10,
    padding: 8,
    backgroundColor: '#eee',
    borderRadius: 8,
  },
  refreshText: {
    fontSize: 12,
    fontWeight: "bold",
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
  hint: {
    fontSize: 11,
    color: "#999",
    marginTop: 8,
    textAlign: "right",
  },
  empty: {
    textAlign: "center",
    marginTop: 40,
    color: "#888",
  },
});
