import { useEffect, useState, type RefObject } from 'react'

type Props = {
  target: RefObject<HTMLDivElement | null>
  onShrink: () => void
  onGrow: () => void
}

export function CardControls({ target, onShrink, onGrow }: Props) {
  const [fullscreen, setFullscreen] = useState(false)

  useEffect(() => {
    const update = () => setFullscreen(document.fullscreenElement === target.current)
    document.addEventListener('fullscreenchange', update)
    return () => document.removeEventListener('fullscreenchange', update)
  }, [target])

  const toggleFullscreen = async () => {
    try {
      if (document.fullscreenElement === target.current) {
        await document.exitFullscreen()
        return
      }
      await target.current?.requestFullscreen()
    } catch {
      setFullscreen(false)
    }
  }

  return (
    <span className="node__card-controls nodrag">
      <button className="node__card-control" onClick={onShrink} title="Kartı küçült" aria-label="Kartı küçült">
        −
      </button>
      <button className="node__card-control" onClick={onGrow} title="Kartı büyüt" aria-label="Kartı büyüt">
        +
      </button>
      <button
        className="node__card-control node__card-control--fullscreen"
        onClick={() => void toggleFullscreen()}
        title={fullscreen ? 'Tam ekrandan çık' : 'Tam ekran'}
        aria-label={fullscreen ? 'Tam ekrandan çık' : 'Tam ekran'}
      >
        {fullscreen ? <CollapseIcon /> : <ExpandIcon />}
      </button>
    </span>
  )
}

function ExpandIcon() {
  return (
    <svg viewBox="0 0 16 16" aria-hidden="true">
      <path d="M2.5 6V2.5H6M10 2.5h3.5V6M13.5 10v3.5H10M6 13.5H2.5V10" />
    </svg>
  )
}

function CollapseIcon() {
  return (
    <svg viewBox="0 0 16 16" aria-hidden="true">
      <path d="M6 2.5V6H2.5M13.5 6H10V2.5M10 13.5V10h3.5M2.5 10H6v3.5" />
    </svg>
  )
}
