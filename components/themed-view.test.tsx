import React from "react";
import { render, screen } from "@testing-library/react-native";
import { Text } from "react-native";
import { ThemedView } from "./themed-view";

// Mock useColorScheme to return a stable value
jest.mock("@/hooks/use-color-scheme", () => ({
  useColorScheme: () => "light",
}));

describe("ThemedView", () => {
  it("renders children without crashing", () => {
    render(
      <ThemedView>
        <Text>Child Content</Text>
      </ThemedView>
    );
    expect(screen.getByText("Child Content")).toBeTruthy();
  });

  it("renders with custom light/dark colors without crashing", () => {
    render(
      <ThemedView lightColor="#fff" darkColor="#000">
        <Text>Themed Content</Text>
      </ThemedView>
    );
    expect(screen.getByText("Themed Content")).toBeTruthy();
  });
});
