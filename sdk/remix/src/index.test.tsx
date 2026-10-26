import { GlobalRegistrator } from "@happy-dom/global-registrator";
import { afterAll, beforeAll, describe, expect, test } from "bun:test";
import { dirname } from "node:path";

const remixReact = dirname(Bun.resolveSync("@remix-run/react", import.meta.dir));
const routerDom = Bun.resolveSync("react-router-dom", remixReact);

beforeAll(() => {
  GlobalRegistrator.register({
    url: "https://shop.supermetry.com/",
    settings: { disableJavaScriptFileLoading: true, handleDisabledFileLoadingAsSuccess: true },
  });
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
});

afterAll(async () => {
  await GlobalRegistrator.unregister();
});

describe("<Supermetry />", () => {
  test("tracks the initial route and every route change in manual mode", async () => {
    const { act } = await import("react");
    const { createRoot } = await import("react-dom/client");
    const { BrowserRouter, useNavigate } = (await import(routerDom)) as typeof import("react-router-dom");
    const { Supermetry } = await import("./index");

    let navigate: ReturnType<typeof useNavigate> = () => {};
    function Navigator() {
      navigate = useNavigate();
      return null;
    }

    const pageviews: unknown[] = [];
    (window as unknown as { supermetry: unknown }).supermetry = {
      track() {},
      pageview: (options: unknown) => pageviews.push(options),
    };

    const root = createRoot(document.body.appendChild(document.createElement("div")));
    await act(async () =>
      root.render(
        <BrowserRouter future={{ v7_startTransition: true, v7_relativeSplatPath: true }}>
          <Supermetry site="site-id" host="https://analytics.supermetry.com" />
          <Navigator />
        </BrowserRouter>,
      ),
    );

    const script = document.head.querySelector("script")!;
    expect(script.getAttribute("src")).toBe("https://analytics.supermetry.com/js/script.full.js");
    expect(script.dataset.manual).toBe("true");

    await act(async () => navigate("/pricing?plan=pro"));
    await act(async () => navigate("/pricing?plan=pro#faq"));
    await act(async () => navigate("/about"));

    expect(pageviews).toEqual([
      { url: "https://shop.supermetry.com/" },
      { url: "https://shop.supermetry.com/pricing?plan=pro" },
      { url: "https://shop.supermetry.com/about" },
    ]);
    act(() => root.unmount());
  });
});
