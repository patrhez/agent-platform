import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { MarkdownContent } from "./MarkdownContent";

describe("MarkdownContent", () => {
  it("renders GFM structure and fenced code", () => {
    const { container } = render(<MarkdownContent content={`# Finding

- first
- second

| File | Status |
| --- | --- |
| api.go | fixed |

\`\`\`go
func main() {}
\`\`\`
`} />);

    expect(screen.getByRole("heading", { name: "Finding" })).toBeTruthy();
    expect(screen.getByRole("list")).toBeTruthy();
    expect(screen.getByRole("table")).toBeTruthy();
    expect(container.querySelector("pre code")?.textContent).toContain("func main");
  });

  it("does not create executable raw HTML", () => {
    const { container } = render(<MarkdownContent content={'<script>window.bad = true</script>'} />);
    expect(container.querySelector("script")).toBeNull();
  });
});
