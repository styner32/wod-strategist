import { Children, cloneElement, isValidElement, type ReactNode } from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { analysisProse, splitTextByTimestamps } from "../analysisPresentation";
import "./analysisMarkdown.css";

export function renderInteractiveChildren(
  children: ReactNode,
  onSeek?: (seconds: number) => void,
): ReactNode {
  if (!onSeek) return children;
  return Children.map(children, (child, index) => {
    if (typeof child === "string") {
      const tokens = splitTextByTimestamps(child);
      if (tokens.length === 1 && tokens[0].type === "text") return child;
      return tokens.map((token, tokenIndex) =>
        token.type === "timestamp" ? (
          <button
            key={`ts-${index}-${tokenIndex}-${token.label}`}
            type="button"
            title={`${token.label} 시점으로 영상 이동`}
            onClick={() => onSeek(token.seconds)}
            className="mx-0.5 inline-flex items-center gap-0.5 rounded border border-accent/30 bg-accent/10 px-1.5 py-0.5 font-mono text-xs font-medium text-accent hover:bg-accent/20 focus-visible:outline-2 focus-visible:outline-accent"
          >
            ▶ {token.label}
          </button>
        ) : (
          token.value
        ),
      );
    }
    if (isValidElement<{ children?: ReactNode }>(child)) {
      const tag = typeof child.type === "string" ? child.type : "";
      if (tag === "code" || tag === "pre" || tag === "a" || tag === "button") {
        return child;
      }
      if (child.props.children) {
        return cloneElement(child, {
          children: renderInteractiveChildren(child.props.children, onSeek),
        });
      }
    }
    return child;
  });
}

export function AnalysisMarkdown({
  text,
  onSeek,
}: {
  text: string;
  onSeek?: (seconds: number) => void;
}) {
  return (
    <div className="analysis-markdown">
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        skipHtml
        components={{
          h1: ({ children }) => (
            <h3>{renderInteractiveChildren(children, onSeek)}</h3>
          ),
          h2: ({ children }) => (
            <h3>{renderInteractiveChildren(children, onSeek)}</h3>
          ),
          h3: ({ children }) => (
            <h4>{renderInteractiveChildren(children, onSeek)}</h4>
          ),
          h4: ({ children }) => (
            <h5>{renderInteractiveChildren(children, onSeek)}</h5>
          ),
          p: ({ children }) => (
            <p>{renderInteractiveChildren(children, onSeek)}</p>
          ),
          li: ({ children }) => (
            <li>{renderInteractiveChildren(children, onSeek)}</li>
          ),
          td: ({ children }) => (
            <td>{renderInteractiveChildren(children, onSeek)}</td>
          ),
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
  onSeek,
}: {
  text: string;
  defaultOpen?: boolean;
  onSeek?: (seconds: number) => void;
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
        <AnalysisMarkdown text={text} onSeek={onSeek} />
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
