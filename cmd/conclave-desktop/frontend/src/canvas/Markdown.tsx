import { memo } from 'react'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { MermaidBlock } from './MermaidBlock'
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
          code: ({ className, children: codeContent, ...props }) => {
            const match = /language-(\w+)/.exec(className || '')
            if (match && match[1] === 'mermaid') {
              return <MermaidBlock chart={String(codeContent).replace(/\n$/, '')} />
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
