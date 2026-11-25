import { useEffect, useRef, useState, type FormEvent, type KeyboardEvent } from 'react'
import type { Message } from '../api'
import { cx } from '../utils'
import Markdown from './Markdown'
import { IconArrowUp, IconMenu, IconStop } from './Icons'

type Props = {
  title: string
  messages: Message[]
  streaming: boolean
  loading: boolean
  error: string
  onDismissError: () => void
  onSend: (text: string) => boolean
  onStop: () => void
  onOpenSidebar: () => void
  disabledReason: string
  conversationKey: string
}

export default function ChatPanel(props: Props) {
  const [text, setText] = useState('')
  const scrollRef = useRef<HTMLDivElement>(null)
  const textareaRef = useRef<HTMLTextAreaElement>(null)
  const stickToBottom = useRef(true)

  const canSend = !props.streaming && !props.disabledReason

  useEffect(() => {
    const element = textareaRef.current
    if (!element) return
    element.style.height = 'auto'
    element.style.height = `${Math.min(element.scrollHeight, 200)}px`
  }, [text])

  useEffect(() => {
    stickToBottom.current = true
    setText('')
    const element = scrollRef.current
    if (element) element.scrollTop = element.scrollHeight
  }, [props.conversationKey])

  useEffect(() => {
    if (!stickToBottom.current) return
    const element = scrollRef.current
    if (element) element.scrollTop = element.scrollHeight
  }, [props.messages, props.streaming, props.loading])

  const onScroll = () => {
    const element = scrollRef.current
    if (!element) return
    stickToBottom.current = element.scrollHeight - element.scrollTop - element.clientHeight < 120
  }

  const submit = (event?: FormEvent) => {
    event?.preventDefault()
    const value = text.trim()
    if (!value || !canSend) return
    if (props.onSend(value)) {
      stickToBottom.current = true
      setText('')
      requestAnimationFrame(() => textareaRef.current?.focus())
    }
  }

  const onKeyDown = (event: KeyboardEvent<HTMLTextAreaElement>) => {
    if (event.key === 'Enter' && !event.shiftKey && !event.nativeEvent.isComposing) {
      event.preventDefault()
      submit()
    }
  }

  const showWelcome = props.messages.length === 0 && !props.loading

  return (
    <section className="chat-panel">
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
            <p>消息会发送到当前选中的 ChatGPT 账号。</p>
          </div>
        ) : (
          <div className="messages">
            {props.messages.map((message) => (
              <article key={message.id} className={cx('msg', message.role)}>
                {message.role === 'assistant' ? (
                  <div className="assistant-body">
                    {message.text ? <Markdown text={message.text} /> : null}
                    {message.streaming && <span className="caret" />}
                  </div>
                ) : (
                  <div className="user-bubble">{message.text}</div>
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
        {props.error && (
          <div className="banner error-banner">
            <span>{props.error}</span>
            <button type="button" onClick={props.onDismissError} aria-label="关闭提示">
              ×
            </button>
          </div>
        )}
        {props.disabledReason && <div className="banner warn-banner">{props.disabledReason}</div>}
        <form className="composer" onSubmit={submit}>
          <textarea
            ref={textareaRef}
            value={text}
            rows={1}
            onChange={(event) => setText(event.target.value)}
            onKeyDown={onKeyDown}
            placeholder={props.disabledReason || '向 ChatGPT 发送消息'}
            aria-label="消息内容"
          />
          <div className="composer-actions">
            {props.streaming ? (
              <button type="button" className="send-btn stop" onClick={props.onStop} aria-label="停止生成" title="停止生成">
                <IconStop />
              </button>
            ) : (
              <button
                type="submit"
                className="send-btn"
                disabled={!text.trim() || !canSend}
                aria-label="发送"
                title="发送"
              >
                <IconArrowUp />
              </button>
            )}
          </div>
        </form>
        <p className="disclaimer">GptWeb 使用 ChatGPT 网页接口，生成内容可能不准确，请核查重要信息。</p>
      </div>
    </section>
  )
}
