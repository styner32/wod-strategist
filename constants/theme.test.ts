import { Colors } from "./theme";

describe("Theme Colors", () => {
  it("should define a light color scheme", () => {
    expect(Colors.light).toBeDefined();
    expect(Colors.light.text).toBeTruthy();
    expect(Colors.light.background).toBeTruthy();
    expect(Colors.light.tint).toBeTruthy();
    expect(Colors.light.icon).toBeTruthy();
    expect(Colors.light.tabIconDefault).toBeTruthy();
    expect(Colors.light.tabIconSelected).toBeTruthy();
  });

  it("should define a dark color scheme", () => {
    expect(Colors.dark).toBeDefined();
    expect(Colors.dark.text).toBeTruthy();
    expect(Colors.dark.background).toBeTruthy();
    expect(Colors.dark.tint).toBeTruthy();
    expect(Colors.dark.icon).toBeTruthy();
    expect(Colors.dark.tabIconDefault).toBeTruthy();
    expect(Colors.dark.tabIconSelected).toBeTruthy();
  });

  it("should have matching keys in light and dark schemes", () => {
    const lightKeys = Object.keys(Colors.light).sort();
    const darkKeys = Object.keys(Colors.dark).sort();
    expect(lightKeys).toEqual(darkKeys);
  });

  it("should match the expected color snapshot", () => {
    expect(Colors).toMatchSnapshot();
  });
});
