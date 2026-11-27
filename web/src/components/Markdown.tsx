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

function codeLanguage(children: ReactNode): string {
  const child = Array.isArray(children) ? children[0] : children
  if (!isValidElement<{ className?: string }>(child)) return ''
  const match = /language-([\w-]+)/.exec(child.props.className || '')
  return match ? match[1] : ''
}

function CodeBlock({ children }: { children?: ReactNode }) {
  const [copied, setCopied] = useState(false)
  const language = codeLanguage(children)
  const codeText = plainText(children)
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(codeText)
      setCopied(true)
      window.setTimeout(() => setCopied(false), 1400)
    } catch {
      setCopied(false)
    }
  }
  return (
    <div className="code-block">
      <div className="code-head">
        <span>{language || '代码'}</span>
        <button type="button" onClick={copy}>
          {copied ? '已复制' : '复制'}
        </button>
      </div>
      <div className="code-scroll">{children}</div>
    </div>
  )
}

// ChatGPT 的 content.parts 使用 LaTeX 的 \[...\] 和 \(...\) 分隔符，
// remark-math 只识别 $...$ / $$...$$；在代码块和行内代码之外做转换。
function convertMathSegment(segment: string): string {
  let result = segment.replace(/\\\[([\s\S]*?)\\\]/g, (_, body: string) => `\n\n$$\n${body.trim()}\n$$\n\n`)
  result = result.replace(/\\\(([\s\S]*?)\\\)/g, (_, body: string) => `$${body}$`)
  return result
}

// genui / image_group 等特殊内容统一用私用区字符包裹：
//    type  {json} 
// 当前客户端不实现这些组件，整段替换为「暂不支持」内联标签。
const UNSUPPORTED_HREF = '#gptweb-unsupported'
const genUIPattern = /([^]*)([\s\S]*?)/g

function replaceGenUISegment(segment: string): string {
  let result = segment.replace(genUIPattern, `[暂不支持](${UNSUPPORTED_HREF})`)
  // 流式输出时标记可能只到一半，先把未闭合的标记隐藏，避免露出私用区字符。
  const openIndex = result.lastIndexOf('')
  const closeIndex = result.lastIndexOf('')
  if (openIndex >= 0 && openIndex > closeIndex) {
    result = result.slice(0, openIndex)
  }
  return result
}

function normalizeContent(value: string): string {
  return value
    .split(/(```[\s\S]*?```)/g)
    .map((part, blockIndex) => {
      if (blockIndex % 2 === 1) return part
      return part
        .split(/(`[^`]*`)/g)
        .map((segment, inlineIndex) => {
          if (inlineIndex % 2 === 1) return segment
          return convertMathSegment(replaceGenUISegment(segment))
        })
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
          a: ({ children, href }) =>
            href === UNSUPPORTED_HREF ? (
              <span className="unsupported-chip">{children}</span>
            ) : (
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
        {normalizeContent(text)}
      </ReactMarkdown>
    </div>
  )
}
