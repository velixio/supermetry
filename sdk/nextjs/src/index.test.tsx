import { GlobalRegistrator } from "@happy-dom/global-registrator";
import { afterAll, beforeAll, describe, expect, test } from "bun:test";
import { renderToString } from "react-dom/server";

beforeAll(() => {
  GlobalRegistrator.register({ settings: { disableJavaScriptFileLoading: true, handleDisabledFileLoadingAsSuccess: true } });
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
});

afterAll(async () => {
  await GlobalRegistrator.unregister();
});

describe("<Supermetry />", () => {
  test("renders nothing on the server", async () => {
    const { Supermetry } = await import("./index");
    expect(renderToString(<Supermetry site="site-id" />)).toBe("");
  });

  test("injects a first-party script by default", async () => {
    const { act } = await import("react");
    const { createRoot } = await import("react-dom/client");
    const { Supermetry, useSupermetry, getTracker } = await import("./index");

    const root = createRoot(document.body.appendChild(document.createElement("div")));
    await act(async () => root.render(<Supermetry site="site-id" outbound />));

    const script = document.head.querySelector("script")!;
    expect(script.getAttribute("src")).toBe("/js/script.js");
    expect(script.dataset.api).toBe("/api/public/v1/events");
    expect(script.dataset.outbound).toBe("true");
    expect(useSupermetry()).toBe(getTracker());
    act(() => root.unmount());
  });
});
