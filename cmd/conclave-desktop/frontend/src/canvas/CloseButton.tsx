/** Shared node close affordance. Sits above the node body and never triggers a
 *  drag, so a click always means "remove this". */
export function CloseButton({ onClose, label }: { onClose: () => void; label: string }) {
  return (
    <button
      className="node__close nodrag"
      title={label}
      aria-label={label}
      onClick={(event) => {
        event.stopPropagation()
        onClose()
      }}
    >
      <svg width="12" height="12" viewBox="0 0 12 12" aria-hidden="true">
        <path
          d="M2.5 2.5l7 7M9.5 2.5l-7 7"
          fill="none"
          stroke="currentColor"
          strokeWidth="1.3"
          strokeLinecap="round"
        />
      </svg>
    </button>
  )
}
