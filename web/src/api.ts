export type Me = {
  username: string
  token_tags: string[]
}

export type ConversationSummary = {
  id: string
  title: string
  create_time?: string
  update_time?: string
  archived?: boolean
}

export type Attachment = {
  id: string
  library_file_id?: string
  name: string
  mime_type: string
  size: number
  width?: number
  height?: number
  kind: 'image' | 'file'
  previewUrl?: string
}

export type Message = {
  id: string
  role: 'user' | 'assistant'
  text: string
  create_time?: number
  streaming?: boolean
  attachments?: Attachment[]
}

export type Conversation = {
  id: string
  title: string
  model?: string
  current_node?: string
  create_time?: string
  update_time?: string
  messages: Message[]
}

export class ApiError extends Error {
  status: number
  constructor(status: number, message: string) {
    super(message)
    this.status = status
  }
}

export async function api<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers)
  if (init.body && !headers.has('Content-Type')) {
    headers.set('Content-Type', 'application/json')
  }
  const response = await fetch(path, { credentials: 'include', ...init, headers })
  if (!response.ok) {
    let message = `请求失败（HTTP ${response.status}）`
    try {
      const data = (await response.json()) as { error?: string }
      if (data.error) message = data.error
    } catch {
      // ignore non-json body
    }
    if (response.status === 401 && path !== '/api/login') {
      window.dispatchEvent(new Event('gptweb:unauthorized'))
    }
    throw new ApiError(response.status, message)
  }
  return (await response.json()) as T
}

export type ChatStreamEvent =
  | { type: 'start' }
  | { type: 'delta'; text: string }
  | { type: 'done'; conversation_id: string; message_id?: string; text: string }
  | { type: 'error'; message: string }

export type ChatRequest = {
  tag: string
  conversation_id?: string
  parent_message_id?: string
  message: string
  attachments?: Attachment[]
}

export async function uploadFile(tag: string, file: File): Promise<Attachment> {
  const form = new FormData()
  form.append('tag', tag)
  form.append('file', file)
  const response = await fetch('/api/upload', { method: 'POST', credentials: 'include', body: form })
  if (!response.ok) {
    let message = `上传失败（HTTP ${response.status}）`
    try {
      const data = (await response.json()) as { error?: string }
      if (data.error) message = data.error
    } catch {
      // ignore
    }
    if (response.status === 401) {
      window.dispatchEvent(new Event('gptweb:unauthorized'))
    }
    throw new ApiError(response.status, message)
  }
  return (await response.json()) as Attachment
}

export function fileURL(attachment: Attachment, tag: string, conversationId?: string): string {
  const params = new URLSearchParams()
  params.set('tag', tag)
  if (conversationId) params.set('conversation_id', conversationId)
  if (attachment.name) params.set('name', attachment.name)
  if (attachment.kind) params.set('kind', attachment.kind)
  return `/api/files/${encodeURIComponent(attachment.id)}?${params.toString()}`
}

export type ChatHandlers = {
  onDelta: (text: string) => void
  onDone: (event: Extract<ChatStreamEvent, { type: 'done' }>) => void
  onError: (message: string) => void
}

export async function streamChat(
  body: ChatRequest,
  handlers: ChatHandlers,
  signal?: AbortSignal,
): Promise<void> {
  const response = await fetch('/api/chat', {
    method: 'POST',
    credentials: 'include',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
    signal,
  })
  if (!response.ok) {
    let message = `发送失败（HTTP ${response.status}）`
    try {
      const data = (await response.json()) as { error?: string }
      if (data.error) message = data.error
    } catch {
      // ignore
    }
    if (response.status === 401) {
      window.dispatchEvent(new Event('gptweb:unauthorized'))
    }
    throw new ApiError(response.status, message)
  }
  if (!response.body) {
    throw new ApiError(500, '服务器没有返回流式响应')
  }
  const reader = response.body.getReader()
  const decoder = new TextDecoder()
  let buffer = ''

  const dispatch = (chunk: string) => {
    for (const line of chunk.split('\n')) {
      if (!line.startsWith('data:')) continue
      const payload = line.slice(5).trim()
      if (!payload) continue
      let event: ChatStreamEvent
      try {
        event = JSON.parse(payload) as ChatStreamEvent
      } catch {
        continue
      }
      if (event.type === 'delta') handlers.onDelta(event.text)
      else if (event.type === 'done') handlers.onDone(event)
      else if (event.type === 'error') handlers.onError(event.message)
    }
  }

  while (true) {
    const { done, value } = await reader.read()
    if (done) break
    buffer += decoder.decode(value, { stream: true })
    let index = buffer.indexOf('\n\n')
    while (index >= 0) {
      dispatch(buffer.slice(0, index))
      buffer = buffer.slice(index + 2)
      index = buffer.indexOf('\n\n')
    }
  }
  if (buffer.trim()) dispatch(buffer)
}
