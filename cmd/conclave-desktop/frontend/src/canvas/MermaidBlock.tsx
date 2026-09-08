import { useEffect, useId, useRef, useState } from 'react'
import mermaid from 'mermaid'

let mermaidInitialized = false

function initMermaid() {
  if (mermaidInitialized) return
  mermaid.initialize({
    startOnLoad: false,
    theme: 'dark',
    securityLevel: 'loose',
    themeVariables: {
      darkMode: true,
      background: '#12151d',
      primaryColor: '#1c2029',
      primaryTextColor: '#e4e8f2',
      primaryBorderColor: '#3a4256',
      lineColor: '#7aa8ff',
      secondaryColor: '#171b25',
      tertiaryColor: '#0b0d12',
    },
  })
  mermaidInitialized = true
}

export function MermaidBlock({ chart }: { chart: string }) {
  const [svg, setSvg] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const containerRef = useRef<HTMLDivElement>(null)
  const uniqueId = useId().replace(/:/g, '')

  useEffect(() => {
    let canceled = false
    initMermaid()

    const renderChart = async () => {
      try {
        setError(null)
        const id = `mermaid-chart-${uniqueId}`
        const { svg: renderedSvg } = await mermaid.render(id, chart)
        if (!canceled) {
          setSvg(renderedSvg)
        }
      } catch (err) {
        if (!canceled) {
          // Keep raw chart fallback while streaming incomplete mermaid syntax
          setError(err instanceof Error ? err.message : 'Diyagram çizilemedi')
        }
      }
    }

    void renderChart()

    return () => {
      canceled = true
    }
  }, [chart, uniqueId])

  if (error || !svg) {
    return (
      <div className="mermaid-fallback nodrag">
        {error && <span className="mermaid-fallback__hint">Mermaid önizleme bekleniyor...</span>}
        <pre className="mermaid-fallback__raw">
          <code>{chart}</code>
        </pre>
      </div>
    )
  }

  return (
    <div
      ref={containerRef}
      className="mermaid-block nodrag"
      dangerouslySetInnerHTML={{ __html: svg }}
    />
  )
}
