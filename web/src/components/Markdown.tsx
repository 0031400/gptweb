import { isValidElement, useState, type ReactNode } from 'react'
import ReactMarkdown from 'react-markdown'
import rehypeKatex from 'rehype-katex'
import remarkGfm from 'remark-gfm'
import remarkMath from 'remark-math'

function plainText(node: ReactNode): string {
  if (node == null || typeof node === 'boolean') return ''
  if (typeof node === 'string' || typeof node === 'number') return String(node)
  if (Array.isArray(node)) return node.map(plainText).join('')
  if (isValidElement<{ children?: ReactNode }>(node)) return plainText(node.props.children)
  return ''
}

function CodeBlock({ children }: { children?: ReactNode }) {
  const [copied, setCopied] = useState(false)
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(plainText(children))
      setCopied(true)
      window.setTimeout(() => setCopied(false), 1400)
    } catch {
      setCopied(false)
    }
  }
  return (
    <div className="code-block">
      <div className="code-head">
        <span>代码</span>
        <button type="button" onClick={copy}>
          {copied ? '已复制' : '复制'}
        </button>
      </div>
      <div className="code-scroll">{children}</div>
    </div>
  )
}

// ChatGPT 的 fallbackMarkdown 使用 LaTeX 的 \[...\] 和 \(...\) 分隔符，
// remark-math 只识别 $...$ / $$...$$；在代码块和行内代码之外做转换。
function convertMathSegment(segment: string): string {
  let result = segment.replace(/\\\[([\s\S]*?)\\\]/g, (_, body: string) => `\n\n$$\n${body.trim()}\n$$\n\n`)
  result = result.replace(/\\\(([\s\S]*?)\\\)/g, (_, body: string) => `$${body}$`)
  return result
}

function normalizeMath(value: string): string {
  return value
    .split(/(```[\s\S]*?```)/g)
    .map((part, blockIndex) => {
      if (blockIndex % 2 === 1) return part
      return part
        .split(/(`[^`]*`)/g)
        .map((segment, inlineIndex) => (inlineIndex % 2 === 1 ? segment : convertMathSegment(segment)))
        .join('')
    })
    .join('')
}

export default function Markdown({ text }: { text: string }) {
  return (
    <div className="markdown">
      <ReactMarkdown
        remarkPlugins={[remarkGfm, remarkMath]}
        rehypePlugins={[rehypeKatex]}
        components={{
          pre: ({ children }) => <CodeBlock>{children}</CodeBlock>,
          a: ({ children, href }) => (
            <a href={href} target="_blank" rel="noreferrer noopener">
              {children}
            </a>
          ),
          table: ({ children }) => (
            <div className="table-wrap">
              <table>{children}</table>
            </div>
          ),
        }}
      >
        {normalizeMath(text)}
      </ReactMarkdown>
    </div>
  )
}
