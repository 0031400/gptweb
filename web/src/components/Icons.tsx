type IconProps = { size?: number }

const base = (size: number) => ({
  width: size,
  height: size,
  viewBox: '0 0 24 24',
  fill: 'none',
  stroke: 'currentColor',
  strokeWidth: 1.8,
  strokeLinecap: 'round' as const,
  strokeLinejoin: 'round' as const,
  'aria-hidden': true,
})

export function IconPlus({ size = 18 }: IconProps) {
  return (
    <svg {...base(size)}>
      <path d="M12 5v14M5 12h14" />
    </svg>
  )
}

export function IconSearch({ size = 18 }: IconProps) {
  return (
    <svg {...base(size)}>
      <circle cx="11" cy="11" r="7" />
      <path d="m20 20-3.2-3.2" />
    </svg>
  )
}

export function IconRefresh({ size = 16 }: IconProps) {
  return (
    <svg {...base(size)}>
      <path d="M20 11a8 8 0 1 0-2.3 5.7" />
      <path d="M20 5v6h-6" />
    </svg>
  )
}

export function IconLogout({ size = 18 }: IconProps) {
  return (
    <svg {...base(size)}>
      <path d="M9 4H6a2 2 0 0 0-2 2v12a2 2 0 0 0 2 2h3" />
      <path d="M15 8l4 4-4 4M19 12H9" />
    </svg>
  )
}

export function IconMenu({ size = 20 }: IconProps) {
  return (
    <svg {...base(size)}>
      <path d="M4 7h16M4 12h16M4 17h16" />
    </svg>
  )
}

export function IconClose({ size = 18 }: IconProps) {
  return (
    <svg {...base(size)}>
      <path d="M6 6l12 12M18 6 6 18" />
    </svg>
  )
}

export function IconTrash({ size = 15 }: IconProps) {
  return (
    <svg {...base(size)}>
      <path d="M4 7h16M9 7V5h6v2M6 7l1 13h10l1-13" />
      <path d="M10 11v5M14 11v5" />
    </svg>
  )
}

export function IconArrowUp({ size = 18 }: IconProps) {
  return (
    <svg {...base(size)}>
      <path d="M12 19V5M6 11l6-6 6 6" />
    </svg>
  )
}

export function IconStop({ size = 15 }: IconProps) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
      <rect x="7" y="7" width="10" height="10" rx="2" />
    </svg>
  )
}

export function IconPaperclip({ size = 18 }: IconProps) {
  return (
    <svg {...base(size)}>
      <path d="M21.4 11.6 12.6 20.4a6 6 0 0 1-8.5-8.5l9.2-9.2a4 4 0 1 1 5.7 5.7l-9.2 9.2a2 2 0 1 1-2.8-2.8l8.5-8.5" />
    </svg>
  )
}

export function IconFile({ size = 18 }: IconProps) {
  return (
    <svg {...base(size)}>
      <path d="M14 3H7a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V8z" />
      <path d="M14 3v5h5M9 13h6M9 17h4" />
    </svg>
  )
}

export function IconImage({ size = 18 }: IconProps) {
  return (
    <svg {...base(size)}>
      <rect x="3" y="4" width="18" height="16" rx="3" />
      <circle cx="8.5" cy="9.5" r="1.5" />
      <path d="m21 16-4.5-4.5L9 19" />
    </svg>
  )
}
