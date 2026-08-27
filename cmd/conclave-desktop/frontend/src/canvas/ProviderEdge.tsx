import { BaseEdge, EdgeLabelRenderer, getSmoothStepPath, type EdgeProps } from '@xyflow/react'

/**
 * A link drawn in the colours of the two cards it joins.
 *
 * Conclave exists to put different models in touch with each other, so the line
 * between them carries both identities: it leaves the source in that provider's
 * colour and arrives in the target's. Which model is talking to which is then
 * readable from the board itself, without labels on every edge.
 *
 * The working mode is in the line's texture rather than its text — a handoff is
 * a thin line, a dialogue a heavier one, a review a dotted one — so the label
 * only appears when the edge is selected and the details actually matter.
 */
export function ProviderEdge({
  id,
  sourceX,
  sourceY,
  targetX,
  targetY,
  sourcePosition,
  targetPosition,
  markerEnd,
  selected,
  data,
}: EdgeProps) {
  const [path, labelX, labelY] = getSmoothStepPath({
    sourceX,
    sourceY,
    targetX,
    targetY,
    sourcePosition,
    targetPosition,
    borderRadius: 14,
  })

  const toNote = Boolean(data?.toNote)
  const mode = (data?.mode as string) ?? 'relay'
  const active = Boolean(data?.active)
  const source = (data?.sourceAccent as string) ?? 'var(--line-strong)'
  const target = (data?.targetAccent as string) ?? 'var(--line-strong)'
  const label = data?.label as string | undefined

  // A line to a result card is a record of where something came from, not a
  // route anything travels. It stays out of the way.
  if (toNote) {
    return (
      <BaseEdge
        id={id}
        path={path}
        className={`edge edge--note${selected ? ' edge--selected' : ''}`}
      />
    )
  }

  const gradient = `edge-gradient-${id}`
  return (
    <>
      <defs>
        <linearGradient
          id={gradient}
          gradientUnits="userSpaceOnUse"
          x1={sourceX}
          y1={sourceY}
          x2={targetX}
          y2={targetY}
        >
          {/* stop-color as a style, not an attribute: a var() reference is CSS
              and does not resolve in an SVG presentation attribute. */}
          <stop offset="0%" style={{ stopColor: source }} />
          <stop offset="100%" style={{ stopColor: target }} />
        </linearGradient>
      </defs>
      <BaseEdge
        id={id}
        path={path}
        markerEnd={markerEnd}
        className={
          `edge edge--${mode}` +
          (active ? ' edge--active' : '') +
          (selected ? ' edge--selected' : '')
        }
        style={{ stroke: `url(#${gradient})` }}
      />
      {selected && label && (
        <EdgeLabelRenderer>
          <div
            className="edge__label nodrag nopan"
            style={{ transform: `translate(-50%, -50%) translate(${labelX}px, ${labelY}px)` }}
          >
            {label}
          </div>
        </EdgeLabelRenderer>
      )}
    </>
  )
}
