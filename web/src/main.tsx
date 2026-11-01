import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'

// Self-hosted fonts: no third-party requests, so the CSP can stay tight
// (no fonts.googleapis.com / fonts.gstatic.com origins to allow).
import '@fontsource/jetbrains-mono/400.css'
import '@fontsource/jetbrains-mono/500.css'

import './index.css'
import App from './App'
import { loadRuntime } from './lib/runtime'

void loadRuntime().then(() => createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App />
  </StrictMode>,
))
