import { useState, type CSSProperties, type ReactNode } from 'react'
import type { GenUIComponent } from '../api'
import ChartWidget from './GenUIChart'
import Markdown from './Markdown'

type RecordValue = Record<string, unknown>

const CONTAINER_TAGS = new Set([
  'box',
  'row',
  'grid',
  'grid-item',
  'text',
  'title',
  'caption',
  'blockquote',
  'spacer',
  'underline',
  'list',
  'list-item',
  'flow',
  'flow-item',
  'carousel',
  'carousel-item',
  'segmented-control',
  'date-picker',
  'divider',
  'icon',
  'badge',
  'popover',
  'popover-trigger',
  'popover-content',
  'button',
  'radio',
  'radio-group',
  'option',
  'checkbox',
  'switch',
  'input',
  'textarea',
  'select',
  'form',
  'card',
  'field',
  'label',
  'slider',
  'CodeBlock',
  'Link',
  'AsyncImage',
])

function asRecord(value: unknown): RecordValue | null {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return null
  return value as RecordValue
}

function stringValue(value: unknown): string {
  return typeof value === 'string' ? value : ''
}

function componentData(component: GenUIComponent): RecordValue {
  return asRecord(component.data) ?? {}
}

function Unimplemented({ type }: { type: string }) {
  return (
    <div className="genui-unimplemented" title={type} role="note">
      未实现
    </div>
  )
}

function CopyButton({ text, className }: { text: string; className?: string }) {
  const [copied, setCopied] = useState(false)
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(text)
      setCopied(true)
      window.setTimeout(() => setCopied(false), 1400)
    } catch {
      setCopied(false)
    }
  }
  return (
    <button type="button" className={className} onClick={copy}>
      {copied ? '已复制' : '复制'}
    </button>
  )
}

function CodeBlockWidget({ data }: { data: RecordValue }) {
  const language = stringValue(data.language) || '代码'
  const code = stringValue(data.code)
  return (
    <div className="code-block">
      <div className="code-head">
        <span>{language}</span>
        <CopyButton text={code} />
      </div>
      <div className="code-scroll">
        <pre>
          <code>{code}</code>
        </pre>
      </div>
    </div>
  )
}

function WritingBlockWidget({ data }: { data: RecordValue }) {
  const subject = stringValue(data.subject) || '文档'
  const variant = stringValue(data.variant) || 'document'
  const body = stringValue(data.body)
  const label = variant === 'email' ? '邮件' : '文档'
  const copyText = `${subject}\n\n${body}`
  return (
    <section className="genui-writing">
      <div className="genui-writing-head">
        <div className="genui-writing-title">
          <span className="genui-writing-kind">{label}</span>
          <strong>{subject}</strong>
        </div>
        <CopyButton text={copyText} className="genui-copy" />
      </div>
      <div className="genui-writing-body">{body}</div>
    </section>
  )
}

function AsyncImageWidget({ data }: { data: RecordValue }) {
  const images = (Array.isArray(data.images) ? data.images : [])
    .map(asRecord)
    .filter((item): item is RecordValue => item !== null)
  const image = images[0]
  const query = stringValue(data.query)
  const title = stringValue(image?.title) || query
  const thumbnail = stringValue(image?.thumbnail_url) || stringValue(image?.content_url)
  const href = stringValue(image?.url) || stringValue(image?.content_url)
  const ratio = stringValue(data.aspectRatio).replace(':', ' / ')
  const style: CSSProperties = {
    width: stringValue(data.width) || '100%',
    maxWidth: stringValue(data.maxWidth) || undefined,
  }
  if (!thumbnail) {
    return <div className="genui-image-placeholder">{query || '图片'}</div>
  }
  const media = (
    <img
      src={thumbnail}
      alt={title || '图片'}
      loading="lazy"
      referrerPolicy="no-referrer"
      style={{ aspectRatio: ratio || undefined }}
    />
  )
  return (
    <figure className="genui-image">
      <div className="genui-image-frame" style={style}>
        {href ? (
          <a href={href} target="_blank" rel="noreferrer noopener">
            {media}
          </a>
        ) : (
          media
        )}
      </div>
      {title ? <figcaption>{title}</figcaption> : null}
    </figure>
  )
}

function LinkWidget({ data }: { data: RecordValue }) {
  const url = stringValue(data.url)
  const title = stringValue(data.title) || url
  if (!url) return <Unimplemented type="link" />
  return (
    <a href={url} target="_blank" rel="noreferrer noopener">
      {title}
    </a>
  )
}

function MapWidget() {
  return <Unimplemented type="map_widget_v2" />
}

function InteractiveBlockWidget() {
  return <Unimplemented type="interactive_block" />
}

function UnknownWidget({ type }: { type: string }) {
  return <Unimplemented type={type} />
}

function GenUIBlock({ component }: { component: GenUIComponent }) {
  const data = componentData(component)
  switch (component.type) {
    case 'code_block':
      return <CodeBlockWidget data={data} />
    case 'writing_block':
      return <WritingBlockWidget data={data} />
    case 'async_image':
      return <AsyncImageWidget data={data} />
    case 'link':
      return <LinkWidget data={data} />
    case 'charts_widget_v2':
      return <ChartWidget data={data} />
    case 'map_widget_v2':
    case 'map':
      return <MapWidget />
    case 'interactive_block':
      return <InteractiveBlockWidget />
    default:
      return <UnknownWidget type={component.type} />
  }
}

function stripContainerTags(value: string): string {
  let output = ''
  let index = 0
  while (index < value.length) {
    if (value[index] !== '<') {
      output += value[index]
      index++
      continue
    }
    let cursor = index + 1
    if (value[cursor] === '/') cursor++
    const nameStart = cursor
    while (cursor < value.length && /[A-Za-z0-9-]/.test(value[cursor])) cursor++
    const name = value.slice(nameStart, cursor)
    if (!CONTAINER_TAGS.has(name)) {
      output += value[index]
      index++
      continue
    }
    let quote = ''
    let braces = 0
    while (cursor < value.length) {
      const char = value[cursor]
      if (quote) {
        if (char === quote) quote = ''
      } else if (char === '"' || char === "'") {
        quote = char
      } else if (char === '{') {
        braces++
      } else if (char === '}') {
        if (braces > 0) braces--
      } else if (char === '>' && braces === 0) {
        cursor++
        break
      }
      cursor++
    }
    index = cursor
  }
  return output
}

function cleanSegment(segment: string): string {
  let result = segment.replace(/<badge\b[^>]*>([\s\S]*?)<\/badge>/gi, '$1')
  result = result.replace(/<icon\b[^>]*\/>|<icon\b[^>]*>[\s\S]*?<\/icon>/gi, '')
  result = result.replace(/<divider\b[^>]*\/>/gi, '')
  result = result.replace(/<AsyncImage\b[^>]*\/>/gi, '')
  return stripContainerTags(result)
}

function cleanInline(line: string): string {
  return line
    .split(/(`[^`]*`)/g)
    .map((segment) => (segment.startsWith('`') ? segment : cleanSegment(segment)))
    .join('')
}

export function cleanGenUIText(text: string): string {
  let inFence = false
  let inQuote = false
  const output: string[] = []
  for (const line of text.split('\n')) {
    const trimmed = line.trim()
    if (trimmed.startsWith('```') || trimmed.startsWith('~~~')) {
      inFence = !inFence
      output.push(line)
      continue
    }
    if (inFence) {
      output.push(line)
      continue
    }
    if (inQuote) {
      const close = trimmed.indexOf('</blockquote>')
      const inner = close >= 0 ? trimmed.slice(0, close) : trimmed
      output.push(inner ? `> ${cleanInline(inner)}` : '>')
      if (close >= 0) inQuote = false
      continue
    }
    const open = trimmed.indexOf('<blockquote')
    if (open >= 0) {
      const tagEnd = trimmed.indexOf('>', open)
      if (tagEnd < 0) {
        output.push(cleanInline(trimmed))
        continue
      }
      const after = trimmed.slice(tagEnd + 1)
      const close = after.indexOf('</blockquote>')
      const inner = close >= 0 ? after.slice(0, close) : after
      output.push(inner ? `> ${cleanInline(inner)}` : '>')
      if (close < 0) inQuote = true
      continue
    }
    // 没有推导出区间的 DIL 声明/条件行也不要直接显示。
    if (/^\s*\{(?:@body|#if|:else|\/if|#each|\/each|@html)\b/.test(line)) {
      output.push('')
      continue
    }
    // GenUI 容器会把正文缩进 4-6 格；去掉这层缩进，避免 Markdown
    // 把普通文字误判成缩进代码块。围栏代码块内部保持原样。
    output.push(cleanInline(line).replace(/^[ \t]{4,}/, ''))
  }
  return output.join('\n')
}

function escapeLinkLabel(label: string): string {
  return label.replace(/\\/g, '\\\\').replace(/[[\]]/g, '\\$&')
}

function markdownLink(title: string, url: string): string {
  const label = escapeLinkLabel(title)
  const target = url.replace(/\(/g, '%28').replace(/\)/g, '%29')
  return `[${label}](${target})`
}

// 上游 start_index/end_index 是 Unicode 码点偏移；JS slice 使用 UTF-16 单元，
// 组件前面出现 emoji 时必须先换算。
function codePointToUTF16(text: string, offset: number): number {
  if (offset <= 0) return 0
  let units = 0
  let count = 0
  for (const char of text) {
    if (count >= offset) break
    units += char.length
    count++
  }
  return Math.min(units, text.length)
}

export default function MessageContent({ text, genui }: { text: string; genui?: GenUIComponent[] }) {
  if (!genui || genui.length === 0) {
    return <Markdown text={cleanGenUIText(text)} />
  }

  const components = [...genui]
    .filter((component) => Number.isFinite(component.start_index) && Number.isFinite(component.end_index))
    .sort((left, right) => left.start_index - right.start_index)

  const pieces: ReactNode[] = []
  let buffer = ''
  let cursor = 0
  let key = 0

  const flush = () => {
    const clean = cleanGenUIText(buffer)
    if (clean.trim()) {
      pieces.push(<Markdown key={`text-${key++}`} text={clean} />)
    }
    buffer = ''
  }

  for (const component of components) {
    const start = Math.max(cursor, codePointToUTF16(text, component.start_index))
    const end = Math.min(text.length, codePointToUTF16(text, component.end_index))
    if (end <= start || start > text.length) continue
    buffer += text.slice(cursor, start)
    if (component.type === 'link') {
      const data = componentData(component)
      const url = stringValue(data.url)
      if (url) {
        buffer += markdownLink(stringValue(data.title) || url, url)
      } else {
        flush()
        pieces.push(<Unimplemented key={`genui-${key++}`} type="link" />)
      }
    } else {
      flush()
      pieces.push(<GenUIBlock key={`genui-${key++}`} component={component} />)
    }
    cursor = end
  }
  buffer += text.slice(cursor)
  flush()

  if (pieces.length === 0) {
    return <Markdown text={cleanGenUIText(text)} />
  }
  return <div className="genui-content">{pieces}</div>
}
