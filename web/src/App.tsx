import { useEffect, useState } from 'react'
import { api, type Me } from './api'
import Login from './components/Login'
import Workspace from './components/Workspace'

export default function App() {
  const [me, setMe] = useState<Me | null>(null)
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    api<Me>('/api/me')
      .then(setMe)
      .catch(() => setMe(null))
      .finally(() => setLoading(false))
  }, [])

  useEffect(() => {
    const handler = () => setMe(null)
    window.addEventListener('gptweb:unauthorized', handler)
    return () => window.removeEventListener('gptweb:unauthorized', handler)
  }, [])

  useEffect(() => {
    if (!me) document.title = 'GptWeb'
  }, [me])

  const logout = async () => {
    try {
      await api('/api/logout', { method: 'POST' })
    } catch {
      // 就算请求失败也回到登录页
    }
    setMe(null)
  }

  if (loading) {
    return (
      <div className="splash">
        <span className="login-logo">G</span>
        <span className="spinner" />
      </div>
    )
  }
  if (!me) return <Login onLogin={setMe} />
  return <Workspace me={me} onLogout={logout} />
}
