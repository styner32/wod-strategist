import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { analysisProse } from "../analysisPresentation";
import "./analysisMarkdown.css";

export function AnalysisMarkdown({ text }: { text: string }) {
  return (
    <div className="analysis-markdown">
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        skipHtml
        components={{
          h1: ({ children }) => <h3>{children}</h3>,
          h2: ({ children }) => <h3>{children}</h3>,
          h3: ({ children }) => <h4>{children}</h4>,
          h4: ({ children }) => <h5>{children}</h5>,
          table: ({ children }) => (
            <div
              className="analysis-table"
              role="region"
              aria-label="분석 표"
              tabIndex={0}
            >
              <table>{children}</table>
            </div>
          ),
          a: ({ href, children }) => (
            <a href={href} target="_blank" rel="noopener noreferrer">
              {children}
            </a>
          ),
        }}
      >
        {analysisProse(text)}
      </ReactMarkdown>
    </div>
  );
}
export function AnalysisOriginal({
  text,
  defaultOpen = false,
}: {
  text: string;
  defaultOpen?: boolean;
}) {
  return (
    <details
      className="mt-5 min-w-0 rounded-lg border border-border p-3"
      open={defaultOpen}
    >
      <summary className="cursor-pointer text-sm text-text-secondary">
        원문 보기
      </summary>
      <div className="mt-4">
        <AnalysisMarkdown text={text} />
      </div>
      <details className="mt-4">
        <summary className="cursor-pointer text-xs text-text-muted">
          저장 원문 · 내부 데이터 포함
        </summary>
        <pre className="mt-3 max-h-96 overflow-auto whitespace-pre-wrap break-all text-xs">
          {text}
        </pre>
      </details>
    </details>
  );
}
