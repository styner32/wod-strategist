import { recordingErrorMessage } from "../recordingErrorMessage";

it("preserves native callback code, message and underlying cause", () => {
  expect(recordingErrorMessage({ code: "capture/create-recorder-error", message: "Audio settings unavailable",
    cause: { message: "Audio session inactive" } })).toBe(
    "[capture/create-recorder-error] Audio settings unavailable (Audio session inactive)",
  );
});

it("handles Error, strings, unknown dictionaries and circular native objects", () => {
  expect(recordingErrorMessage(new Error("disk full"))).toBe("disk full");
  expect(recordingErrorMessage("interrupted")).toBe("interrupted");
  expect(recordingErrorMessage({ reason: "interrupted" })).toBe('{"reason":"interrupted"}');
  const circular: { self?: unknown } = {};
  circular.self = circular;
  expect(recordingErrorMessage(circular)).toBe("unreadable_recording_error");
});
