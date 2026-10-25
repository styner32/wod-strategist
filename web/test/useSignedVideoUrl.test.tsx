jest.mock("react", () => jest.requireActual("../../node_modules/react"));

import { createElement } from "react";
import { useSignedVideoUrl, type SignedVideoUrlOptions } from "../src/history/useSignedVideoUrl";

const { act, create } = require("react-test-renderer");

type Hook = ReturnType<typeof useSignedVideoUrl>;

const flush = () => new Promise((resolve) => setTimeout(resolve, 0));

function renderSignedVideo(refetchUrl: SignedVideoUrlOptions["refetchUrl"]) {
  const video = {
    currentTime: 750,
    playbackRate: 1.5,
    paused: false,
    ended: false,
    error: null as { code: number } | null,
    load: jest.fn(),
  };
  let props: SignedVideoUrlOptions = {
    videoRef: { current: video as unknown as HTMLVideoElement },
    sourceKey: "session-1:merged",
    url: "https://storage.test/merged.mp4?sig=1",
    expiresAt: null,
    fetchedAtMs: Date.now(),
    fetching: false,
    refetchUrl,
    fallbackTime: 0,
  };
  let hook!: Hook;
  function Harness(current: SignedVideoUrlOptions) {
    hook = useSignedVideoUrl(current);
    return null;
  }
  let tree: { update: (element: unknown) => void; unmount: () => void };
  return {
    video,
    get hook() {
      return hook;
    },
    async mount() {
      await act(async () => {
        tree = create(createElement(Harness, props));
      });
    },
    async rerender(next: Partial<SignedVideoUrlOptions>) {
      props = { ...props, ...next };
      await act(async () => tree.update(createElement(Harness, props)));
    },
    async unmount() {
      await act(async () => tree.unmount());
    },
  };
}

describe("useSignedVideoUrl", () => {
  it("caps refetches for an error that recurs after every successful reload", async () => {
    const refetchUrl = jest
      .fn()
      .mockResolvedValueOnce("https://storage.test/merged.mp4?sig=2")
      .mockResolvedValueOnce("https://storage.test/merged.mp4?sig=3");
    const view = renderSignedVideo(refetchUrl);
    await view.mount();
    view.video.error = { code: 2 };

    for (const nextUrl of ["https://storage.test/merged.mp4?sig=2", "https://storage.test/merged.mp4?sig=3"]) {
      await act(async () => {
        view.hook.handleError();
        await flush();
      });
      await view.rerender({ url: nextUrl });
      // loadedmetadata of the fresh URL restores the position once.
      expect(view.hook.takeRestore()).toMatchObject({ currentTime: 750, playbackRate: 1.5, wasPlaying: true });
    }

    await act(async () => {
      view.hook.handleError();
      await flush();
    });
    expect(refetchUrl).toHaveBeenCalledTimes(2);
    expect(view.hook.playbackError).toBe(true);
    await view.unmount();
  });

  it("shows the playback error when the recovery refetch fails or returns the failing URL", async () => {
    const refetchUrl = jest
      .fn()
      .mockRejectedValueOnce(new Error("offline"))
      .mockResolvedValueOnce("https://storage.test/merged.mp4?sig=1");
    const view = renderSignedVideo(refetchUrl);
    await view.mount();
    view.video.error = { code: 2 };

    await act(async () => {
      view.hook.handleError();
      await flush();
    });
    expect(view.hook.playbackError).toBe(true);
    expect(view.hook.pendingRestoreRef.current).toBeNull();

    // The retry button reloads the element when the server signs the same URL.
    view.video.error = null;
    await act(async () => {
      view.hook.reload({ wasPlaying: true });
      await flush();
    });
    expect(view.hook.playbackError).toBe(false);
    expect(view.video.load).toHaveBeenCalledTimes(1);
    expect(view.hook.pendingRestoreRef.current).toMatchObject({ currentTime: 750, wasPlaying: true });
    await view.unmount();
  });

  it("never refetches for a decode error", async () => {
    const refetchUrl = jest.fn();
    const view = renderSignedVideo(refetchUrl);
    await view.mount();
    view.video.error = { code: 3 };

    await act(async () => view.hook.handleError());
    expect(refetchUrl).not.toHaveBeenCalled();
    expect(view.hook.playbackError).toBe(true);
    await view.unmount();
  });

  it("keeps callbacks stable while the playhead advances", async () => {
    const view = renderSignedVideo(jest.fn());
    await view.mount();
    const first = view.hook;
    await view.rerender({ fallbackTime: 12.25, fetching: true });
    await view.rerender({ fallbackTime: 12.5, fetching: false });

    for (const key of ["needsReload", "reload", "handleError", "handlePlay", "handlePause", "takeRestore"] as const) {
      expect(view.hook[key]).toBe(first[key]);
    }
    await view.unmount();
  });
});
