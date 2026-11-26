import {
  useEffect,
  useRef,
  useState,
  type ClipboardEvent,
  type DragEvent,
  type FormEvent,
  type KeyboardEvent,
} from 'react'
import { fileURL, uploadFile, type Attachment, type Message } from '../api'
import { cx, formatBytes, isImageFile } from '../utils'
import Markdown from './Markdown'
import {
  IconArrowUp,
  IconClose,
  IconFile,
  IconMenu,
  IconPaperclip,
  IconStop,
} from './Icons'

type Props = {
  title: string
  tag: string
  conversationId: string
  messages: Message[]
  streaming: boolean
  loading: boolean
  error: string
  onDismissError: () => void
  onSend: (text: string, attachments: Attachment[]) => boolean
  onStop: () => void
  onOpenSidebar: () => void
  disabledReason: string
  conversationKey: string
}

type PendingFile = {
  key: string
  file: File
  previewUrl?: string
}

const MAX_FILES = 8
const MAX_FILE_SIZE = 20 * 1024 * 1024

function MessageAttachments({
  attachments,
  tag,
  conversationId,
}: {
  attachments: Attachment[]
  tag: string
  conversationId: string
}) {
  const images = attachments.filter((item) => item.kind === 'image' && (item.previewUrl || item.id))
  const files = attachments.filter((item) => item.kind !== 'image')
  if (images.length === 0 && files.length === 0) return null
  return (
    <>
      {images.length > 0 && (
        <div className="msg-images">
          {images.map((item) => {
            const url = item.previewUrl || fileURL(item, tag, conversationId)
            return (
              <a key={item.id || item.name} href={url} target="_blank" rel="noreferrer">
                <img src={url} alt={item.name || '图片'} loading="lazy" />
              </a>
            )
          })}
        </div>
      )}
      {files.length > 0 && (
        <div className="msg-files">
          {files.map((item) => {
            const url = fileURL(item, tag, conversationId)
            return (
              <a key={item.id || item.name} className="msg-file" href={url} download={item.name}>
                <IconFile size={17} />
                <span className="msg-file-name">{item.name || item.id}</span>
                <span className="msg-file-size">{formatBytes(item.size)}</span>
              </a>
            )
          })}
        </div>
      )}
    </>
  )
}

export default function ChatPanel(props: Props) {
  const [text, setText] = useState('')
  const [pending, setPending] = useState<PendingFile[]>([])
  const [uploading, setUploading] = useState(false)
  const [attachError, setAttachError] = useState('')
  const [dragging, setDragging] = useState(false)
  const scrollRef = useRef<HTMLDivElement>(null)
  const textareaRef = useRef<HTMLTextAreaElement>(null)
  const fileInputRef = useRef<HTMLInputElement>(null)
  const pendingRef = useRef<PendingFile[]>([])
  const dragDepth = useRef(0)
  const stickToBottom = useRef(true)

  const canSend =
    !props.streaming && !uploading && !props.disabledReason && (text.trim().length > 0 || pending.length > 0)

  useEffect(() => {
    pendingRef.current = pending
  }, [pending])

  useEffect(() => {
    const element = textareaRef.current
    if (!element) return
    element.style.height = 'auto'
    element.style.height = `${Math.min(element.scrollHeight, 200)}px`
  }, [text])

  useEffect(() => {
    stickToBottom.current = true
    setText('')
    pendingRef.current.forEach((item) => {
      if (item.previewUrl) URL.revokeObjectURL(item.previewUrl)
    })
    pendingRef.current = []
    setPending([])
    setAttachError('')
    const element = scrollRef.current
    if (element) element.scrollTop = element.scrollHeight
  }, [props.conversationKey])

  useEffect(() => {
    if (!stickToBottom.current) return
    const element = scrollRef.current
    if (element) element.scrollTop = element.scrollHeight
  }, [props.messages, props.streaming, props.loading])

  useEffect(() => {
    // 防止文件拖到聊天区外时浏览器直接打开文件。
    const prevent = (event: globalThis.DragEvent) => {
      if (event.dataTransfer && Array.from(event.dataTransfer.types).includes('Files')) {
        event.preventDefault()
      }
    }
    window.addEventListener('dragover', prevent)
    window.addEventListener('drop', prevent)
    return () => {
      window.removeEventListener('dragover', prevent)
      window.removeEventListener('drop', prevent)
    }
  }, [])

  const onScroll = () => {
    const element = scrollRef.current
    if (!element) return
    stickToBottom.current = element.scrollHeight - element.scrollTop - element.clientHeight < 120
  }

  const addFiles = (files: FileList | File[]) => {
    const incoming = Array.from(files)
    if (incoming.length === 0) return
    const next = [...pendingRef.current]
    const errors: string[] = []
    for (const file of incoming) {
      if (next.length >= MAX_FILES) {
        errors.push(`一次最多 ${MAX_FILES} 个附件`)
        break
      }
      if (file.size > MAX_FILE_SIZE) {
        errors.push(`「${file.name}」超过 20MB`)
        continue
      }
      if (
        next.some(
          (item) =>
            item.file.name === file.name &&
            item.file.size === file.size &&
            item.file.lastModified === file.lastModified,
        )
      ) {
        continue
      }
      next.push({
        key: `${Date.now()}-${Math.random().toString(36).slice(2)}`,
        file,
        previewUrl: isImageFile(file) ? URL.createObjectURL(file) : undefined,
      })
    }
    pendingRef.current = next
    setPending(next)
    setAttachError(errors[0] ?? '')
  }

  const removePending = (key: string) => {
    const target = pendingRef.current.find((item) => item.key === key)
    if (target?.previewUrl) URL.revokeObjectURL(target.previewUrl)
    const next = pendingRef.current.filter((item) => item.key !== key)
    pendingRef.current = next
    setPending(next)
  }

  const submit = async (event?: FormEvent) => {
    event?.preventDefault()
    if (!canSend || uploading) return
    const value = text.trim()
    const items = pendingRef.current
    if (!value && items.length === 0) return
    let attachments: Attachment[] = []
    if (items.length > 0) {
      setUploading(true)
      setAttachError('')
      try {
        attachments = await Promise.all(
          items.map(async (item) => {
            const attachment = await uploadFile(props.tag, item.file)
            if (item.previewUrl) attachment.previewUrl = item.previewUrl
            return attachment
          }),
        )
      } catch (err) {
        setAttachError(err instanceof Error ? err.message : '上传文件失败')
        setUploading(false)
        return
      }
      setUploading(false)
    }
    if (props.onSend(value, attachments)) {
      stickToBottom.current = true
      setText('')
      pendingRef.current = []
      setPending([])
      setAttachError('')
      requestAnimationFrame(() => textareaRef.current?.focus())
    }
  }

  const onKeyDown = (event: KeyboardEvent<HTMLTextAreaElement>) => {
    if (event.key === 'Enter' && !event.shiftKey && !event.nativeEvent.isComposing) {
      event.preventDefault()
      void submit()
    }
  }

  const onPaste = (event: ClipboardEvent<HTMLTextAreaElement>) => {
    const files = event.clipboardData.files
    if (files.length > 0) {
      event.preventDefault()
      addFiles(files)
    }
  }

  const onDragEnter = (event: DragEvent<HTMLElement>) => {
    if (!event.dataTransfer.types.includes('Files')) return
    event.preventDefault()
    dragDepth.current += 1
    setDragging(true)
  }

  const onDragOver = (event: DragEvent<HTMLElement>) => {
    if (!event.dataTransfer.types.includes('Files')) return
    event.preventDefault()
    event.dataTransfer.dropEffect = 'copy'
  }

  const onDragLeave = (event: DragEvent<HTMLElement>) => {
    if (!event.dataTransfer.types.includes('Files')) return
    dragDepth.current = Math.max(0, dragDepth.current - 1)
    if (dragDepth.current === 0) setDragging(false)
  }

  const onDrop = (event: DragEvent<HTMLElement>) => {
    if (!event.dataTransfer.types.includes('Files')) return
    event.preventDefault()
    dragDepth.current = 0
    setDragging(false)
    addFiles(event.dataTransfer.files)
  }

  const showWelcome = props.messages.length === 0 && !props.loading
  const banner = attachError || props.error

  return (
    <section
      className="chat-panel"
      onDragEnter={onDragEnter}
      onDragOver={onDragOver}
      onDragLeave={onDragLeave}
      onDrop={onDrop}
    >
      <header className="topbar">
        <button type="button" className="icon-btn menu-btn" onClick={props.onOpenSidebar} aria-label="打开菜单">
          <IconMenu />
        </button>
        <div className="model-label">ChatGPT</div>
        <h1 className="sr-only">{props.title}</h1>
        <div className="topbar-right">{props.streaming ? '正在生成…' : ''}</div>
      </header>

      <div className="chat-scroll" ref={scrollRef} onScroll={onScroll}>
        {showWelcome ? (
          <div className="welcome">
            <h2>有什么可以帮忙的？</h2>
            <p>可以发送图片或文件，消息会发送到当前选中的 ChatGPT 账号。</p>
          </div>
        ) : (
          <div className="messages">
            {props.messages.map((message) => (
              <article key={message.id} className={cx('msg', message.role)}>
                {message.role === 'assistant' ? (
                  <div className="assistant-body">
                    {message.text ? <Markdown text={message.text} /> : null}
                    {message.attachments && message.attachments.length > 0 && (
                      <MessageAttachments
                        attachments={message.attachments}
                        tag={props.tag}
                        conversationId={props.conversationId}
                      />
                    )}
                    {message.streaming && <span className="caret" />}
                  </div>
                ) : (
                  <div className="user-bubble">
                    {message.attachments && message.attachments.length > 0 && (
                      <MessageAttachments
                        attachments={message.attachments}
                        tag={props.tag}
                        conversationId={props.conversationId}
                      />
                    )}
                    {message.text ? <div className="user-text">{message.text}</div> : null}
                  </div>
                )}
              </article>
            ))}
            {props.loading && (
              <div className="loading-row">
                <span className="spinner" />
                正在读取会话…
              </div>
            )}
          </div>
        )}
      </div>

      <div className="composer-shell">
        {banner && (
          <div className="banner error-banner">
            <span>{banner}</span>
            <button
              type="button"
              onClick={() => {
                setAttachError('')
                props.onDismissError()
              }}
              aria-label="关闭提示"
            >
              ×
            </button>
          </div>
        )}
        {props.disabledReason && <div className="banner warn-banner">{props.disabledReason}</div>}
        <form className="composer" onSubmit={(event) => void submit(event)}>
          {pending.length > 0 && (
            <div className="pending-list">
              {pending.map((item) => (
                <div className="pending-item" key={item.key}>
                  {item.previewUrl ? (
                    <img className="pending-thumb" src={item.previewUrl} alt="" />
                  ) : (
                    <span className="pending-icon">
                      <IconFile size={17} />
                    </span>
                  )}
                  <span className="pending-name" title={item.file.name}>
                    {item.file.name}
                  </span>
                  <button
                    type="button"
                    className="pending-remove"
                    onClick={() => removePending(item.key)}
                    aria-label={`移除 ${item.file.name}`}
                  >
                    <IconClose size={13} />
                  </button>
                </div>
              ))}
            </div>
          )}
          <textarea
            ref={textareaRef}
            value={text}
            rows={1}
            onChange={(event) => setText(event.target.value)}
            onKeyDown={onKeyDown}
            onPaste={onPaste}
            placeholder={props.disabledReason || '向 ChatGPT 发送消息'}
            aria-label="消息内容"
          />
          <input
            ref={fileInputRef}
            type="file"
            multiple
            hidden
            onChange={(event) => {
              if (event.target.files) addFiles(event.target.files)
              event.target.value = ''
            }}
          />
          <button
            type="button"
            className="composer-attach"
            onClick={() => fileInputRef.current?.click()}
            title="添加图片或文件"
            aria-label="添加图片或文件"
          >
            <IconPaperclip />
          </button>
          <div className="composer-actions">
            {props.streaming ? (
              <button type="button" className="send-btn stop" onClick={props.onStop} aria-label="停止生成" title="停止生成">
                <IconStop />
              </button>
            ) : (
              <button
                type="submit"
                className="send-btn"
                disabled={!canSend || uploading}
                aria-label="发送"
                title="发送"
              >
                {uploading ? <span className="spinner small" /> : <IconArrowUp />}
              </button>
            )}
          </div>
        </form>
        <p className="disclaimer">GptWeb 使用 ChatGPT 网页接口，生成内容可能不准确，请核查重要信息。</p>
      </div>

      <div className={cx('drop-overlay', dragging && 'show')}>
        <IconPaperclip size={22} />
        松开以上传文件
      </div>
    </section>
  )
}
