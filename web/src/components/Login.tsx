import { useState, type FormEvent } from 'react'
import { api, type Me } from '../api'

export default function Login({ onLogin }: { onLogin: (me: Me) => void }) {
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    if (loading) return
    setError('')
    setLoading(true)
    try {
      const me = await api<Me>('/api/login', {
        method: 'POST',
        body: JSON.stringify({ username: username.trim(), password }),
      })
      onLogin(me)
    } catch (err) {
      setError(err instanceof Error ? err.message : '登录失败')
      setLoading(false)
    }
  }

  return (
    <div className="login-page">
      <main className="login-card">
        <span className="login-logo">G</span>
        <h1>登录 GptWeb</h1>
        <p className="login-sub">使用管理员分配的账号继续</p>

        <form onSubmit={submit} className="login-form">
          <label>
            <span>账号</span>
            <input
              value={username}
              onChange={(event) => setUsername(event.target.value)}
              autoComplete="username"
              autoFocus
              required
              placeholder="用户名"
            />
          </label>
          <label>
            <span>密码</span>
            <input
              type="password"
              value={password}
              onChange={(event) => setPassword(event.target.value)}
              autoComplete="current-password"
              required
              placeholder="密码"
            />
          </label>
          {error && <div className="form-error">{error}</div>}
          <button type="submit" className="login-submit" disabled={loading}>
            {loading ? '登录中…' : '继续'}
          </button>
        </form>

        <p className="login-foot">账号与可用 ChatGPT token 由服务端 config.json 管理</p>
      </main>
    </div>
  )
}
