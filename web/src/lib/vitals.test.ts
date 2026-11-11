import { expect, it } from "vitest";
import { rate, show } from "./vitals";

it("rates and shows vitals", () => {
  expect(rate("lcp", 340)).toBe("good");
  expect(rate("lcp", 3000)).toBe("needs work");
  expect(rate("lcp", 7764)).toBe("poor");
  expect(rate("cls", 0.3)).toBe("poor");
  expect(show("lcp", 7764)).toBe("7.8s");
  expect(show("inp", 56)).toBe("56ms");
  expect(show("cls", 0.0479)).toBe("0.05");
});
