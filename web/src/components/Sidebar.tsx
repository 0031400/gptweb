import { useMemo } from 'react'
import type { ConversationSummary } from '../api'
import { cx, initials } from '../utils'
import { IconClose, IconLogout, IconPlus, IconRefresh, IconSearch, IconTrash } from './Icons'

type Props = {
  username: string
  tokens: string[]
  tag: string
  onTagChange: (tag: string) => void
  conversations: ConversationSummary[]
  total: number
  loading: boolean
  activeId: string
  search: string
  onSearch: (value: string) => void
  onSelect: (id: string) => void
  onNew: () => void
  onDelete: (item: ConversationSummary) => void
  onLoadMore: () => void
  onRefresh: () => void
  onLogout: () => void
  open: boolean
  onClose: () => void
  streaming: boolean
}

type Group = { label: string; items: ConversationSummary[] }

function groupConversations(items: ConversationSummary[]): Group[] {
  const now = new Date()
  const startToday = new Date(now.getFullYear(), now.getMonth(), now.getDate()).getTime()
  const day = 86_400_000
  const buckets: Group[] = [
    { label: '今天', items: [] },
    { label: '昨天', items: [] },
    { label: '过去 7 天', items: [] },
    { label: '更早', items: [] },
  ]
  for (const item of items) {
    const raw = item.update_time || item.create_time
    const time = raw ? new Date(raw).getTime() : Number.NaN
    if (Number.isNaN(time)) {
      buckets[3].items.push(item)
      continue
    }
    const startOfItem = new Date(new Date(time).getFullYear(), new Date(time).getMonth(), new Date(time).getDate()).getTime()
    const diffDays = Math.round((startToday - startOfItem) / day)
    if (diffDays <= 0) buckets[0].items.push(item)
    else if (diffDays === 1) buckets[1].items.push(item)
    else if (diffDays <= 7) buckets[2].items.push(item)
    else buckets[3].items.push(item)
  }
  return buckets.filter((bucket) => bucket.items.length > 0)
}

export default function Sidebar(props: Props) {
  const hasMore = props.conversations.length < props.total
  const groups = useMemo(() => groupConversations(props.conversations), [props.conversations])

  return (
    <aside className={cx('sidebar', props.open && 'open')}>
      <div className="sidebar-head">
        <div className="brand">
          <span className="brand-logo">G</span>
          <strong>GptWeb</strong>
        </div>
        <div className="sidebar-head-actions">
          <button type="button" className="icon-btn" onClick={props.onRefresh} title="刷新会话" aria-label="刷新会话">
            <IconRefresh />
          </button>
          <button type="button" className="icon-btn sidebar-close" onClick={props.onClose} aria-label="关闭菜单">
            <IconClose />
          </button>
        </div>
      </div>

      <button type="button" className="new-chat" onClick={props.onNew} disabled={props.streaming || !props.tag}>
        <IconPlus />
        新对话
      </button>

      <div className="search-box">
        <IconSearch size={17} />
        <input
          value={props.search}
          onChange={(event) => props.onSearch(event.target.value)}
          placeholder="搜索聊天"
          aria-label="搜索会话"
        />
      </div>

      <div className="conv-list">
        {props.loading && props.conversations.length === 0 && (
          <div className="conv-skeleton">
            <span />
            <span />
            <span />
          </div>
        )}
        {groups.map((group) => (
          <div className="conv-group" key={group.label}>
            <div className="group-label">{group.label}</div>
            {group.items.map((item) => (
              <div key={item.id} className={cx('conv-item', item.id === props.activeId && 'active')}>
                <button type="button" className="conv-main" onClick={() => props.onSelect(item.id)} title={item.title}>
                  {item.title}
                </button>
                <button
                  type="button"
                  className="conv-delete"
                  onClick={() => props.onDelete(item)}
                  title="删除会话"
                  aria-label={`删除会话 ${item.title}`}
                >
                  <IconTrash />
                </button>
              </div>
            ))}
          </div>
        ))}
        {!props.loading && props.conversations.length === 0 && (
          <p className="empty-small">{props.search ? '没有找到相关会话' : '还没有历史会话'}</p>
        )}
        {hasMore && (
          <button type="button" className="load-more" onClick={props.onLoadMore} disabled={props.loading}>
            {props.loading ? '加载中…' : '加载更多'}
          </button>
        )}
      </div>

      <div className="sidebar-foot">
        <div className="token-line">
          <span className="token-caption">账号</span>
          <div className="token-select-wrap">
            <select
              value={props.tag}
              onChange={(event) => props.onTagChange(event.target.value)}
              aria-label="选择 ChatGPT 账号"
            >
              {props.tokens.length === 0 && <option value="">未分配账号</option>}
              {props.tokens.map((tag) => (
                <option key={tag} value={tag}>
                  {tag}
                </option>
              ))}
            </select>
          </div>
        </div>
        <div className="user-line">
          <span className="avatar">{initials(props.username)}</span>
          <span className="user-name">{props.username}</span>
          <button type="button" className="icon-btn" onClick={props.onLogout} title="退出登录" aria-label="退出登录">
            <IconLogout size={17} />
          </button>
        </div>
      </div>
    </aside>
  )
}
