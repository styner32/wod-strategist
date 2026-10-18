jest.mock("react", () => jest.requireActual("../../node_modules/react"));
jest.mock("react-markdown", () => {
  const React = require("react");
  return ({ children, components }: any) => {
    const P = components?.p ?? "p";
    return React.createElement(P, null, children);
  };
});
jest.mock("remark-gfm", () => () => {});
jest.mock("../src/history/components/analysisMarkdown.css", () => ({}));

import { createElement, useEffect, useState } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ChunkInspector } from "../src/history/components/ChunkInspector";
import { SessionReviewWorkspace } from "../src/history/components/SessionReviewWorkspace";
import { VideoTransportBar, clampSeekTime, formatPreciseTime } from "../src/history/components/VideoTransportBar";
import { AnalysisMarkdown } from "../src/history/components/AnalysisMarkdown";

const { act, create } = require("react-test-renderer");

it("keeps the player and edited performance record mounted when changing panels", async () => {
  const mounted = jest.fn(), unmounted = jest.fn();
  function Player() {
    useEffect(() => { mounted(); return unmounted; }, []);
    return createElement("video", { src: "original.mp4" });
  }
  function Editor() {
    const [value, setValue] = useState("5");
    return createElement("input", { value, onChange: (event: { target: { value: string } }) => setValue(event.target.value) });
  }
  let tree: ReturnType<typeof create>;
  await act(async () => {
    tree = create(createElement(SessionReviewWorkspace, {
      player: createElement(Player),
      panels: [
        { id: "summary", label: "영상 요약", content: createElement("p", {}, "요약") },
        { id: "performance", label: "수행 이력", content: createElement(Editor) },
      ],
    }));
  });
  const tab = (index: number) => tree.root.findAllByProps({ role: "tab" })[index];
  await act(async () => tab(1).props.onClick());
  await act(async () => tree.root.findByType("input").props.onChange({ target: { value: "58" } }));
  await act(async () => tab(0).props.onClick());
  await act(async () => tab(1).props.onClick());
  expect(tree.root.findByType("input").props.value).toBe("58");
  expect(tree.root.findAllByType("video")).toHaveLength(1);
  expect(mounted).toHaveBeenCalledTimes(1);
  expect(unmounted).not.toHaveBeenCalled();
  expect(tree.root.findAllByProps({ role: "tabpanel" }).map((panel: { props: { hidden: boolean } }) => panel.props.hidden)).toEqual([true, false]);
  await act(async () => tree.unmount());
});

it("supports keyboard navigation with a single tab stop and matching panel labels", async () => {
  const focus = jest.fn();
  let tree: ReturnType<typeof create>;
  await act(async () => {
    tree = create(createElement(SessionReviewWorkspace, {
      player: createElement("video"),
      panels: ["summary", "timeline", "details"].map(id => ({ id, label: id, content: createElement("p", {}, id) })),
    }), { createNodeMock: () => ({ focus }) });
  });
  const tabs = () => tree.root.findAllByProps({ role: "tab" });
  const press = async (index: number, key: string) => {
    const preventDefault = jest.fn();
    await act(async () => tabs()[index].props.onKeyDown({ key, preventDefault }));
    expect(preventDefault).toHaveBeenCalled();
  };
  await press(0, "ArrowLeft");
  expect(tabs().map((tab: { props: { tabIndex: number } }) => tab.props.tabIndex)).toEqual([-1, -1, 0]);
  await press(2, "Home");
  await press(0, "ArrowRight");
  const selected = tabs()[1].props;
  const panel = tree.root.findAllByProps({ role: "tabpanel" })[1].props;
  expect(selected["aria-selected"]).toBe(true);
  expect(selected["aria-controls"]).toBe(panel.id);
  expect(panel["aria-labelledby"]).toBe(selected.id);
  expect(focus).toHaveBeenCalledTimes(3);
  await act(async () => tree.unmount());
});

it("supports controlled activePanelId and onSelectPanel for cross-component tab switching", async () => {
  const onSelectPanel = jest.fn();
  let tree: ReturnType<typeof create>;
  await act(async () => {
    tree = create(createElement(SessionReviewWorkspace, {
      player: createElement("video"),
      activePanelId: "guidance",
      onSelectPanel,
      panels: [
        { id: "summary", label: "영상 요약", content: createElement("p", {}, "요약") },
        { id: "guidance", label: "세그먼트·가이드", content: createElement("p", {}, "가이드") },
      ],
    }));
  });
  expect(tree.root.findAllByProps({ role: "tabpanel" }).map((p: { props: { hidden: boolean } }) => p.props.hidden)).toEqual([true, false]);
  const summaryTab = tree.root.findAllByProps({ role: "tab" })[0];
  await act(async () => summaryTab.props.onClick());
  expect(onSelectPanel).toHaveBeenCalledWith("summary");
  await act(async () => tree.unmount());
});

it("renders VideoTransportBar with step buttons, highlight markers, and clamping", async () => {
  expect(clampSeekTime(-5, 100)).toBe(0);
  expect(clampSeekTime(125, 100)).toBe(100);
  expect(clampSeekTime(42.5, 100)).toBe(42.5);
  expect(formatPreciseTime(82.4)).toBe("1:22.4");

  const onSeek = jest.fn();
  const onTogglePlay = jest.fn();
  const onRateChange = jest.fn();

  let tree: ReturnType<typeof create>;
  await act(async () => {
    tree = create(createElement(VideoTransportBar, {
      currentTime: 20,
      duration: 120,
      isPaused: true,
      playbackRate: 1,
      canControl: true,
      chunks: [{ id: 1, chunk_index: 0, media_start_secs: 0, media_end_secs: 60, status: "COMPLETED" } as any],
      highlights: [{ startSeconds: 45, endSeconds: 50, startLabel: "00:45", endLabel: "00:50", type: "worst_form", reason: "허리 주의" }],
      onSeek,
      onTogglePlay,
      onRateChange,
    }));
  });

  const stepMinus5 = tree.root.findByProps({ "aria-label": "-5초 이동" });
  const stepPlus1 = tree.root.findByProps({ "aria-label": "+1초 이동" });
  const stepMinus10AtStart = tree.root.findByProps({ "aria-label": "-10초 이동" });

  await act(async () => stepMinus5.props.onClick());
  expect(onSeek).toHaveBeenCalledWith(15);

  await act(async () => stepPlus1.props.onClick());
  expect(onSeek).toHaveBeenCalledWith(21);

  await act(async () => stepMinus10AtStart.props.onClick());
  expect(onSeek).toHaveBeenCalledWith(10);

  const playBtn = tree.root.findByProps({ "aria-label": "영상 재생" });
  await act(async () => playBtn.props.onClick());
  expect(onTogglePlay).toHaveBeenCalledTimes(1);

  const rateSelect = tree.root.findByProps({ "aria-label": "재생 배속" });
  await act(async () => rateSelect.props.onChange({ target: { value: "1.5" } }));
  expect(onRateChange).toHaveBeenCalledWith(1.5);

  await act(async () => tree.unmount());
});

it("renders clickable timestamp seek buttons in AnalysisMarkdown when onSeek is provided", async () => {
  const onSeek = jest.fn();
  let tree: ReturnType<typeof create>;
  await act(async () => {
    tree = create(createElement(AnalysisMarkdown, {
      text: "운동 중 01:22 구간에서 무릎 각도를 확인하세요.",
      onSeek,
    }));
  });
  const seekBtn = tree.root.findByProps({ title: "01:22 시점으로 영상 이동" });
  await act(async () => seekBtn.props.onClick());
  expect(onSeek).toHaveBeenCalledWith(82);
  await act(async () => tree.unmount());
});

it("mutes the ChunkInspector video element by default", async () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  client.setQueryData(["chunk-play-url", "session-1", 1], {
    download_url: "https://example.com/chunk-0.mp4",
    source_kind: "chunk",
    media_start_secs: 0,
    media_end_secs: 10,
  });

  let tree: ReturnType<typeof create>;
  await act(async () => {
    tree = create(createElement(QueryClientProvider, { client }, createElement(ChunkInspector, {
      sessionId: "session-1",
      chunk: { id: 1, chunk_index: 0, status: "COMPLETED", media_start_secs: 0, media_end_secs: 10 } as any,
      runs: [],
      runsLoading: false,
      creatingRun: false,
      onCreateRun: () => {},
      onCorrect: () => {},
      onUseCandidate: () => {},
    })));
  });

  const video = tree.root.findByType("video");
  expect(video.props.muted).toBe(true);
  await act(async () => tree.unmount());
  client.clear();
});
