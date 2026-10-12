import { GlobalRegistrator } from "@happy-dom/global-registrator";
import { afterAll, beforeAll, describe, expect, test } from "bun:test";
import { renderToString } from "react-dom/server";

let root: { render(node: unknown): void; unmount(): void };

beforeAll(() => {
  GlobalRegistrator.register({ settings: { disableJavaScriptFileLoading: true, handleDisabledFileLoadingAsSuccess: true } });
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
});

afterAll(async () => {
  const { act } = await import("react");
  act(() => root?.unmount());
  await GlobalRegistrator.unregister();
});

describe("@supermetry/react", () => {
  test("renders children on the server without touching the DOM", async () => {
    const { SupermetryProvider } = await import("./index");
    const html = renderToString(
      <SupermetryProvider site="site-id" host="https://analytics.example.com">
        <p>hello</p>
      </SupermetryProvider>,
    );
    expect(html).toBe("<p>hello</p>");
    expect(document.head.querySelector("script")).toBeNull();
  });

  test("injects the script once and forwards hook calls", async () => {
    const { act } = await import("react");
    const { createRoot } = await import("react-dom/client");
    const { SupermetryProvider, useSupermetry } = await import("./index");

    const calls: unknown[][] = [];
    (window as unknown as { supermetry: unknown }).supermetry = {
      track: (...args: unknown[]) => calls.push(["track", ...args]),
      identify: (...args: unknown[]) => calls.push(["identify", ...args]),
    };
    function Child() {
      const supermetry = useSupermetry();
      supermetry.track("Rendered", { count: 1 });
      return <button onClick={() => supermetry.identify("user-1", { plan: "pro" })}>go</button>;
    }

    const container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
    await act(async () => {
      root.render(
        <SupermetryProvider site="site-id" host="https://analytics.example.com" manual>
          <Child />
        </SupermetryProvider>,
      );
    });

    const scripts = document.head.querySelectorAll("script");
    expect(scripts).toHaveLength(1);
    const script = scripts[0]!;
    expect(script.getAttribute("src")).toBe("https://analytics.example.com/js/script.full.js");
    expect(script.dataset.site).toBe("site-id");
    expect(script.dataset.manual).toBe("true");
    container.querySelector("button")!.click();

    expect(calls).toEqual([
      ["track", "Rendered", { count: 1 }, undefined],
      ["identify", "user-1", { plan: "pro" }],
    ]);
  });
});
