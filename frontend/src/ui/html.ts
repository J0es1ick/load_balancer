export function escapeHTML(value: unknown): string {
  return String(value ?? '')
    .replaceAll('&', '&amp;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;')
    .replaceAll('"', '&quot;')
    .replaceAll("'", '&#039;')
}

export function formatNumber(value: number | undefined): string {
  return new Intl.NumberFormat('ru-RU').format(value ?? 0)
}

export function formatDate(value: string | undefined): string {
  if (!value) return '—'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString('ru-RU', { dateStyle: 'short', timeStyle: 'medium' })
}

export function formatBytes(value: number | undefined): string {
  if (!value) return '—'
  if (value < 1024) return `${value} B`
  if (value < 1_048_576) return `${Math.round(value / 1024)} KiB`
  return `${(value / 1_048_576).toFixed(1)} MiB`
}

const iconPaths: Record<string, string> = {
  overview: '<rect x="3" y="3" width="7" height="7" rx="1"/><rect x="14" y="3" width="7" height="4" rx="1"/><rect x="14" y="11" width="7" height="10" rx="1"/><rect x="3" y="14" width="7" height="7" rx="1"/>',
  request: '<path d="M4 12h13"/><path d="m13 8 4 4-4 4"/><path d="M20 5v14"/>',
  routes: '<circle cx="5" cy="5" r="2"/><circle cx="19" cy="5" r="2"/><circle cx="19" cy="19" r="2"/><path d="M7 5h5a4 4 0 0 1 4 4v8"/>',
  clusters: '<rect x="3" y="4" width="18" height="5" rx="2"/><rect x="3" y="15" width="18" height="5" rx="2"/><path d="M7 9v6M17 9v6"/>',
  config: '<path d="M4 6h16M4 12h16M4 18h16"/><circle cx="9" cy="6" r="2"/><circle cx="15" cy="12" r="2"/><circle cx="11" cy="18" r="2"/>',
  guide: '<path d="M4 5.5A2.5 2.5 0 0 1 6.5 3H11v17H6.5A2.5 2.5 0 0 0 4 22V5.5ZM20 5.5A2.5 2.5 0 0 0 17.5 3H13v17h4.5A2.5 2.5 0 0 1 20 22V5.5Z"/>',
  arrow: '<path d="M5 12h14M13 6l6 6-6 6"/>',
  power: '<path d="M12 2v10"/><path d="M6.3 5.7a8 8 0 1 0 11.4 0"/>',
  pulse: '<path d="M3 12h4l2-6 4 12 2-6h6"/>',
  shield: '<path d="M12 3 4.5 6v5c0 5 3 8.3 7.5 10 4.5-1.7 7.5-5 7.5-10V6L12 3Z"/><path d="m9 12 2 2 4-5"/>',
}

export function icon(name: keyof typeof iconPaths, label = ''): string {
  const aria = label ? `role="img" aria-label="${escapeHTML(label)}"` : 'aria-hidden="true"'
  return `<svg class="icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" ${aria}>${iconPaths[name]}</svg>`
}

export function statusDot(state: 'ok' | 'warn' | 'off'): string {
  return `<span class="status-dot status-dot--${state}" aria-hidden="true"></span>`
}
