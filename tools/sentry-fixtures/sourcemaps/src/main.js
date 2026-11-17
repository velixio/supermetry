import { checkout } from "./cart.js";

export function run(items) {
  return checkout(items);
}

window.run = run;
