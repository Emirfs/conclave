import { memo, useEffect, useRef, useState } from 'react'
import { NodeResizer, type NodeProps } from '@xyflow/react'

import { CardControls } from './CardControls'
import { CloseButton } from './CloseButton'
import { domain } from '../../wailsjs/go/models'
import type { PipelineNodeData } from './useCanvas'

type Props = NodeProps & {
  data: PipelineNodeData & {
    onSave: (pipelineID: number, config: domain.PipelineConfig) => Promise<void>
    onRun: (pipelineID: number) => Promise<void>
    onPickProject: (pipelineID: number, current: string) => Promise<void>
    onClose: (id: string) => void
    onResize: (id: string, direction: -1 | 1) => void
  }
}

const STATUS_LABELS: Record<string, string> = {
  queued: 'sırada',
  running: 'çalışıyor',
  passed: 'geçti',
  failed: 'kaldı',
  blocked: 'atlandı',
  canceled: 'durduruldu',
}

/** A pipeline card: an ordered list of commands run in a project, stopping at
 *  the first failure. It has no provider and no transcript — deterministic work
 *  is exactly what a conversation card is not. */
export const PipelineNode = memo(function PipelineNode({ id, data, selected }: Props) {
  const { pipeline, onSave, onRun, onPickProject, onClose, onResize } = data
  const card = useRef<HTMLDivElement>(null)
  const saved = pipeline.stages ?? []
  const [stages, setStages] = useState<domain.PipelineStage[]>(saved)
  const [title, setTitle] = useState(pipeline.title)
  // Adopt server state when the board reloads, so a save made elsewhere shows.
  useEffect(() => setStages(pipeline.stages ?? []), [pipeline.stages])
  useEffect(() => setTitle(pipeline.title), [pipeline.title])

  const project = pipeline.project_path ?? ''
  const runs = pipeline.runs ?? []
  const latest = runs[0]
  const working = latest?.status === 'queued' || latest?.status === 'running'

  const update = (index: number, patch: Partial<domain.PipelineStage>) =>
    setStages((current) =>
      current.map((stage, at) => (at === index ? { ...stage, ...patch } : stage)),
    )

  const save = () =>
    void onSave(pipeline.id, {
      title,
      project_path: project,
      stages: stages.filter((stage) => stage.command.trim() !== ''),
    } as domain.PipelineConfig)

  return (
    <div
      ref={card}
      className={`node node--pipeline${selected ? ' node--selected' : ''}`}
    >
      <NodeResizer
        minWidth={320}
        minHeight={220}
        isVisible={selected}
        lineClassName="node__resize-line"
        handleClassName="node__resize-handle node__resize-handle--large"
      />
      <header className="node__header node__grip">
        <input
          className="node__pipeline-title nodrag"
          value={title}
          onChange={(event) => setTitle(event.target.value)}
          onKeyDown={(event) => event.stopPropagation()}
          onBlur={save}
          placeholder="Pipeline adı"
          spellCheck={false}
        />
        <CardControls
          target={card}
          onShrink={() => onResize(id, -1)}
          onGrow={() => onResize(id, 1)}
        />
        <CloseButton onClose={() => onClose(id)} label="Pipeline'ı sil" />
      </header>

      <div className="node__toolbar">
        <button
          className="node__chip nodrag"
          onClick={() => void onPickProject(pipeline.id, project)}
          title={project || 'Komutların çalışacağı dizini seç'}
        >
          {project ? project.split(/[\\/]/).pop() : 'proje seç'}
        </button>
        <button
          className="node__run nodrag"
          onClick={() => void onRun(pipeline.id)}
          disabled={working || project === '' || saved.length === 0}
          title={
            project === ''
              ? 'Önce bir proje seç'
              : saved.length === 0
                ? 'Önce en az bir adım kaydet'
                : 'Adımları sırayla çalıştır'
          }
        >
          {working ? 'çalışıyor…' : '▶ çalıştır'}
        </button>
      </div>

      <div className="node__body nowheel">
        <div className="pipeline__stages">
          {stages.length === 0 && <p className="pipeline__empty">Henüz adım yok.</p>}
          {stages.map((stage, index) => (
            <div className="pipeline__stage" key={index}>
              <span className="pipeline__index">{index + 1}</span>
              <input
                className="pipeline__name nodrag"
                value={stage.name}
                onChange={(event) => update(index, { name: event.target.value })}
                onKeyDown={(event) => event.stopPropagation()}
                placeholder="ad"
                spellCheck={false}
              />
              <input
                className="pipeline__command nodrag"
                value={stage.command}
                onChange={(event) => update(index, { command: event.target.value })}
                onKeyDown={(event) => event.stopPropagation()}
                placeholder="go test ./..."
                spellCheck={false}
              />
              <button
                className="pipeline__drop nodrag"
                onClick={() => setStages((current) => current.filter((_, at) => at !== index))}
                title="Adımı sil"
              >
                ✕
              </button>
            </div>
          ))}
          <div className="pipeline__row">
            <button
              className="pipeline__add nodrag"
              onClick={() => setStages((current) => [...current, { name: '', command: '' }])}
            >
              Adım ekle
            </button>
            <button className="pipeline__save nodrag" onClick={save}>
              Kaydet
            </button>
          </div>
        </div>

        {latest && (
          <div className="pipeline__result">
            <span className="pipeline__label">son çalışma</span>
            {(latest.stages ?? []).map((stage) => (
              <StageResult key={stage.id} stage={stage} />
            ))}
          </div>
        )}
      </div>
    </div>
  )
})

/** One stage of the last run. A failure carries its output: the reason a
 *  pipeline stopped is the only thing anyone reads it for. */
function StageResult({ stage }: { stage: domain.Stage }) {
  const [open, setOpen] = useState(stage.status === 'failed')
  const output = stage.output ?? ''
  return (
    <div className={`pipeline__stage-result pipeline__stage-result--${stage.status}`}>
      <button
        className="pipeline__stage-head nodrag"
        onClick={() => setOpen((current) => !current)}
        disabled={output === ''}
      >
        <span className="pipeline__stage-name">{stage.name}</span>
        <span className="pipeline__stage-status">
          {STATUS_LABELS[stage.status] ?? stage.status}
          {stage.status === 'failed' && stage.exit_code !== undefined
            ? ` · exit ${stage.exit_code}`
            : ''}
        </span>
      </button>
      {open && output !== '' && <pre className="pipeline__output nowheel">{output}</pre>}
    </div>
  )
}
