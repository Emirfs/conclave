import { memo, useState } from 'react'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { MermaidBlock } from './MermaidBlock'

function CodeBlock({ language, code }: { language?: string; code: string }) {
  const [copied, setCopied] = useState(false)

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(code)
      setCopied(true)
      setTimeout(() => setCopied(false), 1600)
    } catch {
      // ignore
    }
  }

  return (
    <div className="code-block nodrag">
      <div className="code-block__header">
        <span className="code-block__lang">{language || 'kod'}</span>
        <button
          className={`code-block__copy${copied ? ' code-block__copy--copied' : ''}`}
          onClick={copy}
          type="button"
          title="Kodu panoya kopyala"
        >
          {copied ? 'kopyalandı ✓' : 'kopyala'}
        </button>
      </div>
      <pre className="code-block__pre">
        <code>{code}</code>
      </pre>
    </div>
  )
}

export const Markdown = memo(function Markdown({ children }: { children: string }) {
  return (
    <div className="markdown">
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        components={{
          a: ({ href, children: linkText }) => (
            <a href={href} target="_blank" rel="noreferrer">
              {linkText}
            </a>
          ),
          pre: ({ children: preChildren }) => {
            return <>{preChildren}</>
          },
          code: ({ className, children: codeContent, ...props }) => {
            const match = /language-(\w+)/.exec(className || '')
            const text = String(codeContent).replace(/\n$/, '')
            if (match && match[1] === 'mermaid') {
              return <MermaidBlock chart={text} />
            }
            if (match || text.includes('\n')) {
              return <CodeBlock language={match?.[1]} code={text} />
            }
            return (
              <code className={className} {...props}>
                {codeContent}
              </code>
            )
          },
        }}
      >
        {children}
      </ReactMarkdown>
    </div>
  )
})
