import ReactMarkdown from "react-markdown";
import rehypeKatex from "rehype-katex";
import remarkGfm from "remark-gfm";
import remarkMath from "remark-math";
import "katex/dist/katex.min.css";

export function MarkdownContent({ content }: { content: string }) {
  return <div className="markdown">
    <ReactMarkdown remarkPlugins={[remarkGfm, remarkMath]} rehypePlugins={[rehypeKatex]}>
      {normalizeLatexDelimiters(content)}
    </ReactMarkdown>
  </div>;
}

function normalizeLatexDelimiters(content: string): string {
  const lines = content.split("\n");
  let blockFence = "";
  let inlineCodeTicks = 0;
  return lines.map((line) => {
    const fence = line.match(/^ {0,3}(`{3,}|~{3,})/)?.[1] ?? "";
    if (fence && inlineCodeTicks === 0) {
      if (!blockFence) blockFence = fence;
      else if (fence[0] === blockFence[0] && fence.length >= blockFence.length) blockFence = "";
      return line;
    }
    if (blockFence) return line;
    const normalized = normalizeLineDelimiters(line, inlineCodeTicks);
    inlineCodeTicks = normalized.inlineCodeTicks;
    return normalized.content;
  }).join("\n");
}

function normalizeLineDelimiters(
  line: string,
  initialInlineCodeTicks: number,
): { content: string; inlineCodeTicks: number } {
  let content = "";
  let inlineCodeTicks = initialInlineCodeTicks;
  for (let index = 0; index < line.length;) {
    if (line[index] === "`") {
      let end = index + 1;
      while (line[end] === "`") end++;
      const tickCount = end - index;
      if (inlineCodeTicks === 0) inlineCodeTicks = tickCount;
      else if (inlineCodeTicks === tickCount) inlineCodeTicks = 0;
      content += line.slice(index, end);
      index = end;
      continue;
    }
    const delimiter = inlineCodeTicks === 0 ? latexDelimiterAt(line, index) : undefined;
    if (delimiter) {
      content += delimiter.replacement;
      index += delimiter.length;
      continue;
    }
    content += line[index];
    index++;
  }
  return { content, inlineCodeTicks };
}

function latexDelimiterAt(
  content: string,
  index: number,
): { replacement: string; length: number } | undefined {
  if (content[index] !== "\\" || content[index - 1] === "\\") return undefined;
  const delimiter = content[index + 1];
  if (delimiter === "(" || delimiter === ")") return { replacement: "$", length: 2 };
  if (delimiter === "[" || delimiter === "]") return { replacement: "$$", length: 2 };
  return undefined;
}
