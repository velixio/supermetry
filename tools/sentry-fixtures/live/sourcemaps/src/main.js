import * as Sentry from "@sentry/browser";
import { checkout } from "./cart.js";

Sentry.init({ dsn: window.__DSN__, release: window.__RELEASE__, environment: "fixture" });

window.__run = () => {
  setTimeout(() => checkout([{ price: 60, qty: 2 }]), 20);
};
