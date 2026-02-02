import React, { useEffect, useState } from "react";
import {
  ActivityIndicator,
  StyleSheet,
  Text,
  TouchableOpacity,
  View,
} from "react-native";
import { AnalysisResult, fetchAnalysisHistory } from "../history";

export function HistoryList() {
  const [data, setData] = useState<AnalysisResult[]>([]);
  const [loading, setLoading] = useState(true);
  const [expandedId, setExpandedId] = useState<number | null>(null);

  const loadData = async () => {
    setLoading(true);
    try {
      const history = await fetchAnalysisHistory();
      setData(history);
    } catch (e) {
      console.error(e);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    loadData();
  }, []);

  const toggleExpand = (id: number) => {
    setExpandedId(prev => (prev === id ? null : id));
  };

  const renderItem = (item: AnalysisResult) => {
    const isExpanded = expandedId === item.id;
    return (
      <TouchableOpacity 
        key={item.id} 
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

  if (loading && data.length === 0) {
    return <ActivityIndicator style={{ flex: 1 }} color="#000" />;
  }

  return (
    <View style={styles.list}>
      <TouchableOpacity onPress={loadData} style={styles.refreshBtn}>
        <Text style={styles.refreshText}>🔄 Refresh History</Text>
      </TouchableOpacity>
      
      {data.length === 0 ? (
        <Text style={styles.empty}>No workout history found.</Text>
      ) : (
        data.map(renderItem)
      )}
    </View>
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
