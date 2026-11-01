// A small, dependency-free toast queue for transient confirmation of
// mutating actions (pause, ack, channel test, …), announced politely.

import { createContext, useCallback, useContext, useRef, useState, type ReactNode } from 'react'

type ToastTone = 'neutral' | 'ok' | 'warn' | 'danger'

interface ToastItem {
  id: number
  tone: ToastTone
  message: string
}

interface ToastContextValue {
  push: (message: string, tone?: ToastTone) => void
}

const ToastContext = createContext<ToastContextValue | null>(null)

const TONE: Record<ToastTone, string> = {
  neutral: 'bg-surface border-default text-body',
  ok: 'bg-ok-bg border-ok/30 text-ok',
  warn: 'bg-warn-bg border-warn/30 text-warn',
  danger: 'bg-danger-bg border-danger/30 text-danger',
}

const TTL_MS = 4500

export function ToastProvider({ children }: { children: ReactNode }) {
  const [items, setItems] = useState<ToastItem[]>([])
  const nextId = useRef(0)

  const dismiss = useCallback((id: number) => setItems(list => list.filter(t => t.id !== id)), [])

  const push = useCallback((message: string, tone: ToastTone = 'neutral') => {
    const id = ++nextId.current
    setItems(list => [...list.slice(-3), { id, tone, message }])
    window.setTimeout(() => dismiss(id), TTL_MS)
  }, [dismiss])

  return (
    <ToastContext.Provider value={{ push }}>
      {children}
      <div
        className="fixed bottom-4 inset-x-4 sm:inset-x-auto sm:right-4 z-[60] flex flex-col gap-2 sm:w-96"
        aria-live="polite"
        aria-atomic="false"
      >
        {items.map(t => (
          <div
            key={t.id}
            role="status"
            className={`flex items-start gap-2 rounded-md border px-3.5 py-2.5 text-[13px] shadow-lg animate-fade-in ${TONE[t.tone]}`}
          >
            <span className="flex-1 leading-relaxed break-words">{t.message}</span>
            <button type="button" onClick={() => dismiss(t.id)} aria-label="Dismiss" className="opacity-60 hover:opacity-100 leading-none text-base">
              &times;
            </button>
          </div>
        ))}
      </div>
    </ToastContext.Provider>
  )
}

export function useToast(): ToastContextValue {
  const ctx = useContext(ToastContext)
  if (!ctx) throw new Error('useToast() must be used within <ToastProvider>')
  return ctx
}
