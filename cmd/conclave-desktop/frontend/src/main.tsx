import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'

import './theme.css'
import './app.css'
import { App } from './App'

const container = document.getElementById('root')
if (!container) {
  throw new Error('root element is missing')
}

createRoot(container).render(
  <StrictMode>
    <App />
  </StrictMode>,
)
