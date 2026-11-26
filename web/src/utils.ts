export function planLabel(plan?: string): string {
  if (!plan) return ''
  const map: Record<string, string> = {
    free: '免费版',
    plus: 'Plus',
    pro: 'Pro',
    team: 'Team',
    business: 'Business',
    enterprise: 'Enterprise',
    edu: 'Edu',
  }
  return map[plan.toLowerCase()] ?? plan
}

export function initials(value: string): string {
  const trimmed = value.trim()
  if (!trimmed) return '?'
  const parts = trimmed.split(/\s+/)
  if (parts.length > 1) return (parts[0][0] + parts[1][0]).toUpperCase()
  return trimmed.slice(0, 2).toUpperCase()
}

export function cx(...values: Array<string | false | null | undefined>): string {
  return values.filter(Boolean).join(' ')
}

export function formatBytes(value: number): string {
  if (!value) return ''
  if (value < 1024) return `${value} B`
  if (value < 1024 * 1024) return `${(value / 1024).toFixed(value < 10 * 1024 ? 1 : 0)} KB`
  return `${(value / (1024 * 1024)).toFixed(1)} MB`
}

export function isImageFile(file: File): boolean {
  if (file.type.startsWith('image/')) return true
  return /\.(png|jpe?g|gif|webp|bmp|svg)$/i.test(file.name)
}
