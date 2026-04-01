import React from "react";
import { Platform, StyleSheet, Text, View } from "react-native";

import { Fonts } from "@/constants/theme";

/**
 * Lightweight markdown renderer for Gemini's Korean coaching output.
 *
 * Supported syntax:
 * - ## Heading 2, ### Heading 3
 * - **bold** text, inline within paragraphs
 * - Numbered lists (1. item) and bullet lists (- item)
 * - Horizontal rules (---)
 * - ```code blocks``` (collapsed / de-emphasized)
 */

interface MarkdownTextProps {
  children: string;
  /** Base text color (defaults to #E0E0E0 for dark theme) */
  color?: string;
}

interface ParsedBlock {
  type: "h2" | "h3" | "paragraph" | "numbered" | "bullet" | "hr" | "code";
  content: string;
  /** For numbered lists */
  number?: string;
}

function parseBlocks(raw: string): ParsedBlock[] {
  const lines = raw.split("\n");
  const blocks: ParsedBlock[] = [];
  let inCode = false;
  let codeBuffer: string[] = [];

  for (const line of lines) {
    // Code block fences
    if (line.trimStart().startsWith("```")) {
      if (inCode) {
        // End code block
        blocks.push({ type: "code", content: codeBuffer.join("\n") });
        codeBuffer = [];
        inCode = false;
      } else {
        inCode = true;
      }
      continue;
    }

    if (inCode) {
      codeBuffer.push(line);
      continue;
    }

    const trimmed = line.trim();

    if (trimmed === "") continue;

    // Horizontal rule
    if (/^---+$/.test(trimmed)) {
      blocks.push({ type: "hr", content: "" });
      continue;
    }

    // ### Heading 3
    if (trimmed.startsWith("### ")) {
      blocks.push({ type: "h3", content: trimmed.slice(4) });
      continue;
    }

    // ## Heading 2
    if (trimmed.startsWith("## ")) {
      blocks.push({ type: "h2", content: trimmed.slice(3) });
      continue;
    }

    // Numbered list: "1. ", "2. ", etc.
    const numberedMatch = trimmed.match(/^(\d+)\.\s+(.*)/);
    if (numberedMatch) {
      blocks.push({
        type: "numbered",
        number: numberedMatch[1],
        content: numberedMatch[2],
      });
      continue;
    }

    // Bullet list: "- item" or "* item"
    const bulletMatch = trimmed.match(/^[-*]\s+(.*)/);
    if (bulletMatch) {
      blocks.push({ type: "bullet", content: bulletMatch[1] });
      continue;
    }

    // Regular paragraph
    blocks.push({ type: "paragraph", content: trimmed });
  }

  // Flush remaining code
  if (codeBuffer.length > 0) {
    blocks.push({ type: "code", content: codeBuffer.join("\n") });
  }

  return blocks;
}

/**
 * Renders inline **bold** and regular text segments.
 */
function renderInlineText(text: string, color: string): React.ReactNode[] {
  const parts = text.split(/(\*\*[^*]+\*\*)/g);
  return parts.map((part, i) => {
    if (part.startsWith("**") && part.endsWith("**")) {
      return (
        <Text key={i} style={[styles.bold, { color }]}>
          {part.slice(2, -2)}
        </Text>
      );
    }
    return (
      <Text key={i} style={{ color }}>
        {part}
      </Text>
    );
  });
}

export function MarkdownText({ children, color = "#E0E0E0" }: MarkdownTextProps) {
  if (!children) return null;

  const blocks = parseBlocks(children);

  return (
    <View style={styles.container}>
      {blocks.map((block, i) => {
        switch (block.type) {
          case "hr":
            return <View key={i} style={styles.hr} />;

          case "h2":
            return (
              <Text key={i} style={[styles.h2, { color }]}>
                {renderInlineText(block.content, color)}
              </Text>
            );

          case "h3":
            return (
              <Text key={i} style={[styles.h3, { color }]}>
                {renderInlineText(block.content, color)}
              </Text>
            );

          case "numbered":
            return (
              <View key={i} style={styles.listItem}>
                <Text style={[styles.listNumber, { color: "#64D2FF" }]}>
                  {block.number}.
                </Text>
                <Text style={[styles.listText, { color }]}>
                  {renderInlineText(block.content, color)}
                </Text>
              </View>
            );

          case "bullet":
            return (
              <View key={i} style={styles.listItem}>
                <Text style={[styles.bullet, { color: "#64D2FF" }]}>•</Text>
                <Text style={[styles.listText, { color }]}>
                  {renderInlineText(block.content, color)}
                </Text>
              </View>
            );

          case "code":
            // Hide JSON data blocks (highlights, injury_timestamps) — they're not user-facing
            if (
              block.content.trim().startsWith("[") ||
              block.content.trim().startsWith("{")
            ) {
              return null;
            }
            return (
              <View key={i} style={styles.codeBlock}>
                <Text style={styles.codeText}>{block.content}</Text>
              </View>
            );

          case "paragraph":
          default:
            return (
              <Text key={i} style={[styles.paragraph, { color }]}>
                {renderInlineText(block.content, color)}
              </Text>
            );
        }
      })}
    </View>
  );
}

const monoFont = Fonts?.mono ?? "monospace";

const styles = StyleSheet.create({
  container: {
    gap: 6,
  },
  h2: {
    fontSize: 17,
    fontWeight: "700",
    marginTop: 12,
    marginBottom: 2,
    lineHeight: 24,
  },
  h3: {
    fontSize: 15,
    fontWeight: "600",
    marginTop: 8,
    marginBottom: 2,
    lineHeight: 22,
  },
  paragraph: {
    fontSize: 14,
    lineHeight: 21,
  },
  bold: {
    fontWeight: "700",
  },
  listItem: {
    flexDirection: "row",
    alignItems: "flex-start",
    paddingLeft: 4,
    gap: 8,
  },
  listNumber: {
    fontSize: 14,
    fontWeight: "600",
    lineHeight: 21,
    minWidth: 20,
  },
  bullet: {
    fontSize: 14,
    lineHeight: 21,
    minWidth: 14,
  },
  listText: {
    fontSize: 14,
    lineHeight: 21,
    flex: 1,
  },
  hr: {
    height: 1,
    backgroundColor: "rgba(255,255,255,0.1)",
    marginVertical: 12,
  },
  codeBlock: {
    backgroundColor: "rgba(0,0,0,0.3)",
    borderRadius: 8,
    padding: 12,
    marginVertical: 4,
  },
  codeText: {
    fontSize: 12,
    lineHeight: 18,
    color: "#A0A0A0",
    fontFamily: monoFont,
    ...Platform.select({
      android: { fontFamily: "monospace" },
    }),
  },
});
