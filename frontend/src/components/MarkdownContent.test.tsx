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

  it("renders dollar and backslash LaTeX delimiters with KaTeX", () => {
    const { container } = render(<MarkdownContent content={`Inline: $E = mc^2$

$$
\\int_0^1 x^2 \\, dx = \\frac{1}{3}
$$

Also inline: \\(a^2 + b^2 = c^2\\)

\\[
e^{i\\pi} + 1 = 0
\\]`} />);

    expect(container.querySelectorAll(".katex")).toHaveLength(4);
    expect(container.querySelectorAll(".katex-display")).toHaveLength(2);
    expect(container.querySelectorAll(".katex-mathml")).toHaveLength(4);
  });

  it("leaves LaTeX delimiters inside code unchanged", () => {
    const { container } = render(<MarkdownContent content={`Inline code: \`\\(not math\\)\`

\`\`\`text
\\[also not math\\]
\`\`\``} />);

    expect(container.querySelector(".katex")).toBeNull();
    expect(container.textContent).toContain("\\(not math\\)");
    expect(container.textContent).toContain("\\[also not math\\]");
  });
});
