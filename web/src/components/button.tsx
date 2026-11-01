// Button and Spinner — the base controls every other primitive builds on.

import type { ButtonHTMLAttributes, ReactNode } from 'react'

// ── Button ──────────────────────────────────────────────────────────────────

type Variant = 'primary' | 'secondary' | 'ghost' | 'danger' | 'success'
type Size = 'sm' | 'md'

const VARIANT: Record<Variant, string> = {
  primary: 'bg-accent hover:bg-accent-hover text-accent-fg border-transparent',
  secondary: 'bg-surface-2 hover:bg-surface-hover text-body-2 border-default hover:border-strong',
  ghost: 'bg-transparent hover:bg-surface-hover text-muted hover:text-body border-transparent',
  danger: 'bg-danger hover:brightness-110 text-ground border-transparent',
  success: 'bg-ok hover:brightness-110 text-ground border-transparent',
}

const SIZE: Record<Size, string> = {
  sm: 'h-9 px-3 text-[12px] gap-2 rounded-[10px]',
  md: 'h-10 px-4 text-[13px] gap-2 rounded-[10px]',
}

/** The class string of a button, for a link or label that must look like one. */
export function buttonClass(variant: Variant = 'secondary', size: Size = 'md', extra = ''): string {
  return `spring-button inline-flex items-center justify-center font-medium border whitespace-nowrap
        transition-colors duration-150 disabled:opacity-50 disabled:cursor-not-allowed
        ${VARIANT[variant]} ${SIZE[size]} ${extra}`
}

export function Button({
  variant = 'secondary', size = 'md', busy, icon, children, className = '', type = 'button', ...rest
}: {
  variant?: Variant; size?: Size; busy?: boolean; icon?: ReactNode
} & ButtonHTMLAttributes<HTMLButtonElement>) {
  return (
    <button
      {...rest}
      type={type}
      aria-busy={busy || undefined}
      disabled={rest.disabled || busy}
      className={buttonClass(variant, size, className)}
    >
      {busy ? <Spinner /> : icon}
      {children}
    </button>
  )
}

export function Spinner({ label }: { label?: string }) {
  return (
    <>
      <span aria-hidden="true" className="w-3.5 h-3.5 rounded-full border-2 border-current border-t-transparent animate-spin shrink-0" />
      {label && <span className="sr-only">{label}</span>}
    </>
  )
}
