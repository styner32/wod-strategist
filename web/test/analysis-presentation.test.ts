import {
  analysisProse,
  analysisSegments,
  formatLongTimestamps,
  legacyOverallSummary,
} from "../src/history/analysisPresentation";

describe("analysis presentation", () => {
  it("separates screenshot metadata while retaining Markdown and the original", () => {
    const raw =
      '### 개선 솔루션\n1. **바벨을 가깝게:** 유지하세요.\n\n```injury_timestamps\n[{"start":"01:22","end":"01:24","reason":"확인"}]\n```\n\n```highlights\n[{"type":"positive_form"}]\n```\n\n```mobility\n[{"joint":"Ankle"}]\n```\n\n---\n\n---\n\n후속 설명';
    const prose = analysisProse(raw);
    expect(prose).toContain("**바벨을 가깝게:**");
    expect(prose).toContain("### 개선 솔루션");
    expect(prose).not.toContain("injury_timestamps");
    expect(prose).not.toContain("positive_form");
    expect(prose).toContain("후속 설명");
    expect(raw).toContain("injury_timestamps");
  });
  it.each([
    "```highlights\n{bad json}\n\n### 이후 제목\n보존",
    "```score\n{broken\n\n일반 본문 보존",
    "<mobility>\n{broken\n\n일반 본문 보존",
  ])("preserves prose following an unclosed internal block", (raw) => {
    expect(analysisProse(raw)).toContain("보존");
    expect(analysisProse(raw)).not.toContain("broken");
  });
  it("preserves ordinary JSON and unknown code and supports XML", () => {
    expect(analysisProse('```json\n{"hello":"world"}\n```')).toContain("hello");
    expect(analysisProse('```python\nprint("hello")\n```')).toContain("print");
    expect(analysisProse('<score>{"score":10}</score>\n본문')).toBe("본문");
    expect(analysisProse('```json\n{"score":{"total":10}}\n```\n본문')).toBe(
      "본문",
    );
  });
  it("never maps a segment-like heading inside a code fence", () => {
    const text =
      "```text\n## 세그먼트 1: Fake (00:00 ~ 00:10)\n```\n## 세그먼트 2: Snatch (01:00.100 ~ 01:20.500)\n**설명**";
    expect(analysisSegments(text)).toEqual([
      { title: "Snatch", start: 60.1, end: 80.5, body: "**설명**\n" },
    ]);
  });
  it("does not close a fence when trailing info text is present", () => {
    expect(analysisSegments("```text\n```not-a-close\n## 세그먼트 1: Fake (00:00 ~ 00:10)\n``` ")).toEqual([]);
  });
  it("only uses an explicit legacy overall_summary", () => {
    expect(legacyOverallSummary("처음 열 줄은 요약이 아닙니다")).toBeNull();
    expect(legacyOverallSummary('{"summary":"ambiguous"}')).toBeNull();
    expect(legacyOverallSummary('{"overall_summary":"전체 요약"}')).toBe(
      "전체 요약",
    );
  });
  it("shortens long decimal timestamps in headers and prose down to at most 2 decimal places", () => {
    const raw = "## 세그먼트 1: Power Clean (6:20.0666665 ~ 6:50.0666665)\n본문 내용";
    expect(formatLongTimestamps(raw)).toBe(
      "## 세그먼트 1: Power Clean (6:20.07 ~ 6:50.07)\n본문 내용",
    );
    expect(analysisProse(raw)).toContain(
      "## 세그먼트 1: Power Clean (6:20.07 ~ 6:50.07)",
    );
    expect(analysisSegments(raw)).toEqual([
      { title: "Power Clean", start: 380.07, end: 410.07, body: "본문 내용\n" },
    ]);
  });
  it("carries rounded-up timestamps into the next minute and hour", () => {
    expect(formatLongTimestamps("6:59.998 1:05:59.999 0:59:59.9951 06:30.5049")).toBe(
      "7:00 1:06:00 1:00:00 06:30.5",
    );
    const raw = "## 세그먼트 1: A (6:30.5 ~ 6:59.9983333)\n본문";
    expect(analysisSegments(raw)).toEqual([
      { title: "A", start: 390.5, end: 420, body: "본문\n" },
    ]);
  });
});
