import React from "react";
import { render, screen } from "@testing-library/react-native";
import { ThemedText } from "./themed-text";

// Mock useColorScheme to return a stable value
jest.mock("@/hooks/use-color-scheme", () => ({
  useColorScheme: () => "light",
}));

describe("ThemedText", () => {
  it("renders default text without crashing", () => {
    render(<ThemedText>Hello World</ThemedText>);
    expect(screen.getByText("Hello World")).toBeTruthy();
  });

  it('renders with type="title" without crashing', () => {
    render(<ThemedText type="title">Title Text</ThemedText>);
    expect(screen.getByText("Title Text")).toBeTruthy();
  });

  it('renders with type="subtitle" without crashing', () => {
    render(<ThemedText type="subtitle">Subtitle Text</ThemedText>);
    expect(screen.getByText("Subtitle Text")).toBeTruthy();
  });

  it('renders with type="link" without crashing', () => {
    render(<ThemedText type="link">Link Text</ThemedText>);
    expect(screen.getByText("Link Text")).toBeTruthy();
  });

  it('renders with type="defaultSemiBold" without crashing', () => {
    render(<ThemedText type="defaultSemiBold">SemiBold Text</ThemedText>);
    expect(screen.getByText("SemiBold Text")).toBeTruthy();
  });
});
