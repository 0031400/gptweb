import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import {
  api,
  streamChat,
  type Conversation,
  type ConversationSummary,
  type Me,
  type Message,
  type TokenStatus,
} from '../api'
import ChatPanel from './ChatPanel'
import Sidebar from './Sidebar'
import { cx } from '../utils'

export default function Workspace({ me, onLogout }: { me: Me; onLogout: () => void }) {
  const [tokens, setTokens] = useState<TokenStatus[]>([])
  const [tokenError, setTokenError] = useState('')
  const [tag, setTag] = useState(() => {
    const saved = window.localStorage.getItem('gptweb.tag')
    return saved && me.token_tags.includes(saved) ? saved : (me.token_tags[0] ?? '')
  })
  const [conversations, setConversations] = useState<ConversationSummary[]>([])
  const [total, setTotal] = useState(0)
  const [listLoading, setListLoading] = useState(false)
  const [listError, setListError] = useState('')
  const [chat, setChat] = useState<Conversation | null>(null)
  const [messages, setMessages] = useState<Message[]>([])
  const [convId, setConvId] = useState('')
  const [parentId, setParentId] = useState('')
  const [streaming, setStreaming] = useState(false)
  const [error, setError] = useState('')
  const [search, setSearch] = useState('')
  const [sidebarOpen, setSidebarOpen] = useState(false)
  const [loadingChat, setLoadingChat] = useState(false)
  const [confirmItem, setConfirmItem] = useState<ConversationSummary | null>(null)
  const abortRef = useRef<AbortController | null>(null)
  const tagRef = useRef(tag)

  useEffect(() => {
    tagRef.current = tag
    if (tag) window.localStorage.setItem('gptweb.tag', tag)
  }, [tag])

  useEffect(() => {
    document.title = chat?.title ? `${chat.title} · GptWeb` : 'GptWeb'
  }, [chat?.title])

  const loadTokens = useCallback(async () => {
    try {
      const data = await api<{ tokens: TokenStatus[] }>('/api/tokens')
      setTokens(data.tokens)
      setTokenError('')
      setTag((current) => {
        if (current && data.tokens.some((token) => token.tag === current)) return current
        return data.tokens[0]?.tag ?? ''
      })
    } catch (err) {
      setTokenError(err instanceof Error ? err.message : '读取账号列表失败')
    }
  }, [])

  useEffect(() => {
    void loadTokens()
  }, [loadTokens])

  const loadConversations = useCallback(
    async (offset: number, replace: boolean) => {
      if (!tag) return
      const requestedTag = tag
      setListLoading(true)
      setListError('')
      try {
        const data = await api<{ items: ConversationSummary[]; total: number }>(
          `/api/conversations?tag=${encodeURIComponent(tag)}&offset=${offset}&limit=30`,
        )
        if (tagRef.current !== requestedTag) return
        setConversations((prev) => {
          if (replace) return data.items
          const seen = new Set(prev.map((item) => item.id))
          return [...prev, ...data.items.filter((item) => !seen.has(item.id))]
        })
        setTotal(data.total)
        if (replace) {
          setChat((prev) => {
            if (!prev) return prev
            const match = data.items.find((item) => item.id === prev.id)
            return match && match.title !== prev.title ? { ...prev, title: match.title } : prev
          })
        }
      } catch (err) {
        if (tagRef.current === requestedTag) {
          setListError(err instanceof Error ? err.message : '读取会话列表失败')
        }
      } finally {
        if (tagRef.current === requestedTag) setListLoading(false)
      }
    },
    [tag],
  )

  useEffect(() => {
    abortRef.current?.abort()
    abortRef.current = null
    setStreaming(false)
    setChat(null)
    setMessages([])
    setConvId('')
    setParentId('')
    setError('')
    setConversations([])
    setTotal(0)
    setSearch('')
    if (!tag) return
    void loadConversations(0, true)
  }, [tag, loadConversations])

  const activeToken = tokens.find((token) => token.tag === tag)
  const filteredConversations = useMemo(() => {
    const keyword = search.trim().toLowerCase()
    if (!keyword) return conversations
    return conversations.filter((item) => item.title.toLowerCase().includes(keyword))
  }, [conversations, search])

  const openConversation = async (id: string) => {
    if (streaming || !tag) return
    setLoadingChat(true)
    setError('')
    setSidebarOpen(false)
    try {
      const conversation = await api<Conversation>(
        `/api/conversations/${encodeURIComponent(id)}?tag=${encodeURIComponent(tag)}`,
      )
      setChat(conversation)
      setMessages(conversation.messages.map((message) => ({ ...message, streaming: false })))
      setConvId(conversation.id)
      setParentId(conversation.current_node || conversation.messages.at(-1)?.id || '')
    } catch (err) {
      setError(err instanceof Error ? err.message : '读取会话失败')
    } finally {
      setLoadingChat(false)
    }
  }

  const newChat = () => {
    if (streaming) return
    setChat(null)
    setMessages([])
    setConvId('')
    setParentId('')
    setError('')
    setSidebarOpen(false)
  }

  const finishAssistant = (id: string) => {
    setMessages((prev) =>
      prev.flatMap((message) => {
        if (message.id !== id) return [message]
        if (!message.text) return []
        return [{ ...message, streaming: false }]
      }),
    )
  }

  const sendMessage = (text: string): boolean => {
    if (streaming || !tag || !activeToken?.ready) return false
    const content = text.trim()
    if (!content) return false
    const userId = `local-${Date.now()}`
    const assistantId = `stream-${Date.now()}`
    setMessages((prev) => [
      ...prev,
      { id: userId, role: 'user', text: content, create_time: Date.now() / 1000 },
      { id: assistantId, role: 'assistant', text: '', streaming: true },
    ])
    setStreaming(true)
    setError('')
    const controller = new AbortController()
    abortRef.current = controller
    let streamed = ''

    void streamChat(
      {
        tag,
        conversation_id: convId || undefined,
        parent_message_id: parentId || undefined,
        message: content,
      },
      {
        onDelta: (delta) => {
          streamed += delta
          setMessages((prev) =>
            prev.map((message) =>
              message.id === assistantId ? { ...message, text: message.text + delta } : message,
            ),
          )
        },
        onDone: (event) => {
          const finalText = event.text || streamed
          setMessages((prev) =>
            prev.flatMap((message) => {
              if (message.id !== assistantId) return [message]
              const text = finalText || message.text
              if (!text) return []
              return [{ ...message, id: event.message_id || message.id, text, streaming: false }]
            }),
          )
          if (event.conversation_id) {
            setConvId(event.conversation_id)
            setParentId(event.message_id || '')
            setChat((prev) => prev ?? { id: event.conversation_id, title: '新对话', messages: [] })
          }
        },
        onError: (message) => {
          setError(message)
          finishAssistant(assistantId)
        },
      },
      controller.signal,
    )
      .catch((err: unknown) => {
        if (err instanceof DOMException && err.name === 'AbortError') {
          finishAssistant(assistantId)
          return
        }
        setError(err instanceof Error ? err.message : '发送失败')
        finishAssistant(assistantId)
      })
      .finally(() => {
        finishAssistant(assistantId)
        setStreaming(false)
        abortRef.current = null
        void loadConversations(0, true)
        window.setTimeout(() => {
          void loadConversations(0, true)
        }, 2500)
      })
    return true
  }

  const stopStreaming = () => {
    abortRef.current?.abort()
  }

  const deleteConversation = async () => {
    if (!confirmItem) return
    const target = confirmItem
    setConfirmItem(null)
    try {
      await api(`/api/conversations/${encodeURIComponent(target.id)}?tag=${encodeURIComponent(tag)}`, {
        method: 'DELETE',
      })
      setConversations((prev) => prev.filter((item) => item.id !== target.id))
      setTotal((value) => Math.max(0, value - 1))
      if (convId === target.id) {
        newChat()
      }
    } catch (err) {
      setListError(err instanceof Error ? err.message : '删除会话失败')
    }
  }

  const dismissErrors = () => {
    setError('')
    setListError('')
    setTokenError('')
  }

  const disabledReason = !tag
    ? '还没有可用的 ChatGPT 账号，请联系管理员分配 token'
    : !activeToken
      ? '正在加载账号信息…'
      : !activeToken.ready
        ? activeToken.error || '当前账号未就绪，请先更新 session 文件'
        : ''

  return (
    <div className="workspace">
      <Sidebar
        username={me.username}
        tokens={tokens}
        tag={tag}
        onTagChange={setTag}
        conversations={filteredConversations}
        total={total}
        loading={listLoading}
        activeId={convId}
        search={search}
        onSearch={setSearch}
        onSelect={openConversation}
        onNew={newChat}
        onDelete={setConfirmItem}
        onLoadMore={() => void loadConversations(conversations.length, false)}
        onRefresh={() => {
          void loadTokens()
          void loadConversations(0, true)
        }}
        onLogout={onLogout}
        open={sidebarOpen}
        onClose={() => setSidebarOpen(false)}
        streaming={streaming}
      />
      <div className={cx('sidebar-scrim', sidebarOpen && 'show')} onClick={() => setSidebarOpen(false)} />
      <ChatPanel
        title={chat?.title || '新对话'}
        messages={messages}
        streaming={streaming}
        loading={loadingChat}
        error={error || listError || tokenError}
        onDismissError={dismissErrors}
        onSend={sendMessage}
        onStop={stopStreaming}
        onOpenSidebar={() => setSidebarOpen(true)}
        disabledReason={disabledReason}
        conversationKey={convId || 'new'}
      />
      {confirmItem && (
        <div className="modal-backdrop" onClick={() => setConfirmItem(null)}>
          <div className="modal" role="dialog" aria-modal="true" onClick={(event) => event.stopPropagation()}>
            <h3>删除会话</h3>
            <p>
              确定删除「{confirmItem.title}」吗？这会同时从你的 ChatGPT 账号中删除，且无法恢复。
            </p>
            <div className="modal-actions">
              <button type="button" className="btn-ghost" onClick={() => setConfirmItem(null)}>
                取消
              </button>
              <button type="button" className="btn-danger" onClick={() => void deleteConversation()}>
                删除
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}
