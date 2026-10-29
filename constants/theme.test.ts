import { Colors, getTacticalFatigueTone, TacticalTheme } from "./theme";

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

describe("TacticalTheme & getTacticalFatigueTone", () => {
  it("defines matching semantic keys across light and dark modes", () => {
    const lightKeys = Object.keys(TacticalTheme.light).sort();
    const darkKeys = Object.keys(TacticalTheme.dark).sort();
    expect(lightKeys).toEqual(darkKeys);
    expect(TacticalTheme.light.canvas).toBe("#F1F4F9");
    expect(TacticalTheme.light.volt).toBe("#D4FF00");
  });

  it("maps fatigue scores and state strings to semantic tones", () => {
    expect(getTacticalFatigueTone(15, "light").fg).toBe(
      TacticalTheme.light.statusFresh,
    );
    expect(getTacticalFatigueTone(40, "light").fg).toBe(
      TacticalTheme.light.statusModerate,
    );
    expect(getTacticalFatigueTone(68, "light").fg).toBe(
      TacticalTheme.light.statusFatigued,
    );
    expect(getTacticalFatigueTone(90, "light").fg).toBe(
      TacticalTheme.light.statusExhausted,
    );
    expect(getTacticalFatigueTone("exhausted", "dark").fg).toBe(
      TacticalTheme.dark.statusExhausted,
    );
  });
});
