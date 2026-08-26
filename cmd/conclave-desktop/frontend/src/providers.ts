/** Display identity for each provider the daemon can discover. */
export interface ProviderStyle {
  label: string
  accent: string
  /** Short glyph shown in the badge when there is no room for the label. */
  glyph: string
}

const styles: Record<string, ProviderStyle> = {
  claude: { label: 'Claude', accent: 'var(--provider-claude)', glyph: 'CL' },
  openai: { label: 'Codex', accent: 'var(--provider-openai)', glyph: 'CX' },
  gemini: { label: 'Antigravity', accent: 'var(--provider-gemini)', glyph: 'AG' },
  ollama: { label: 'Ollama', accent: 'var(--provider-ollama)', glyph: 'OL' },
  mnemo: { label: 'Mnemo', accent: 'var(--provider-unknown)', glyph: 'MN' },
}

export function providerStyle(name: string): ProviderStyle {
  return (
    styles[name] ?? {
      label: name,
      accent: 'var(--provider-unknown)',
      glyph: name.slice(0, 2).toUpperCase(),
    }
  )
}
