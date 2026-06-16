export type ThemeChoice = 'system' | 'light' | 'dark'

const STORAGE_KEY = 'dongled.theme'

export function readTheme(): ThemeChoice {
  try {
    const v = globalThis.localStorage?.getItem(STORAGE_KEY)
    if (v === 'light' || v === 'dark' || v === 'system') return v
  } catch {
    return 'system'
  }
  return 'system'
}

export function applyTheme(choice: ThemeChoice): void {
  const root = globalThis.document?.documentElement
  if (!root) return
  if (choice === 'system') root.removeAttribute('data-theme')
  else root.setAttribute('data-theme', choice)
  try {
    globalThis.localStorage?.setItem(STORAGE_KEY, choice)
  } catch {
    return
  }
}
