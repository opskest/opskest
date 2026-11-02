// DropdownMenu — the "More" menu for row actions. An icon button (or any
// trigger) opens an anchored menu with roving focus: arrows, Home/End and
// type-ahead move, Enter/Space choose, Escape closes and returns focus to the
// trigger, Tab closes. The menu is a fixed-position layer, so a card or table
// that clips its overflow cannot cut it off.

import { useEffect, useId, useRef, useState, type KeyboardEvent, type ReactNode } from 'react'
import { Icon } from './icons'
import { FloatingLayer, Tooltip, type Align } from './overlay'

export type MenuEntry =
  | {
    key: string
    label: string
    icon?: ReactNode
    onSelect: () => void
    tone?: 'danger'
    disabled?: boolean
    /** Right-aligned hint, e.g. a shortcut. */
    hint?: ReactNode
  }
  | { key: string; separator: true }

type Item = Exclude<MenuEntry, { separator: true }>

export function DropdownMenu({
  label, items, align = 'end', trigger, className = '', tooltip = true,
}: {
  /** Accessible name of the menu and its default trigger, e.g. "Actions for api-gateway". */
  label: string
  items: MenuEntry[]
  align?: Align
  /** Custom trigger content; the default is a "more" icon button. */
  trigger?: ReactNode
  className?: string
  tooltip?: boolean
}) {
  const [open, setOpen] = useState(false)
  const [anchor, setAnchor] = useState<HTMLElement | null>(null)
  const [active, setActive] = useState(0)
  const triggerRef = useRef<HTMLButtonElement>(null)
  const layerRef = useRef<HTMLDivElement>(null)
  const typed = useRef({ text: '', at: 0 })
  const menuId = useId()

  const actions = items.filter((i): i is Item => !('separator' in i))
  const enabled = actions.map((a, index) => ({ a, index })).filter(x => !x.a.disabled).map(x => x.index)

  function openMenu(at: 'first' | 'last') {
    if (enabled.length === 0) return
    setActive(at === 'first' ? enabled[0] : enabled[enabled.length - 1])
    setAnchor(triggerRef.current)
    setOpen(true)
  }
  function close(refocus = true) {
    setOpen(false)
    setAnchor(null)
    if (refocus) triggerRef.current?.focus()
  }

  // Move DOM focus with the roving index.
  useEffect(() => {
    if (!open) return
    layerRef.current?.querySelectorAll<HTMLElement>('[role="menuitem"]')[active]?.focus()
  }, [open, active, anchor])

  // Outside press closes without stealing focus back from what was pressed.
  useEffect(() => {
    if (!open) return
    function onDown(e: PointerEvent) {
      const t = e.target as Node
      if (layerRef.current?.contains(t) || triggerRef.current?.contains(t)) return
      close(false)
    }
    document.addEventListener('pointerdown', onDown, true)
    return () => document.removeEventListener('pointerdown', onDown, true)
  }, [open])

  function onTriggerKey(e: KeyboardEvent<HTMLButtonElement>) {
    if (e.key === 'ArrowDown') { e.preventDefault(); openMenu('first') }
    else if (e.key === 'ArrowUp') { e.preventDefault(); openMenu('last') }
  }

  function choose(item: Item) {
    if (item.disabled) return
    // Focus the trigger first so a dialog opened by the action restores focus to it.
    close(true)
    item.onSelect()
  }

  function move(delta: number) {
    const at = enabled.indexOf(active)
    setActive(enabled[(at + delta + enabled.length) % enabled.length])
  }

  function onMenuKey(e: KeyboardEvent<HTMLDivElement>) {
    if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); close(); return }
    if (e.key === 'Tab') { close(true); return }
    if (e.key === 'ArrowDown') { e.preventDefault(); move(1) }
    else if (e.key === 'ArrowUp') { e.preventDefault(); move(-1) }
    else if (e.key === 'Home') { e.preventDefault(); setActive(enabled[0]) }
    else if (e.key === 'End') { e.preventDefault(); setActive(enabled[enabled.length - 1]) }
    else if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); const item = actions[active]; if (item) choose(item) }
    else if (e.key.length === 1 && !e.ctrlKey && !e.metaKey && !e.altKey) {
      const now = Date.now()
      typed.current = { text: (now - typed.current.at < 700 ? typed.current.text : '') + e.key.toLowerCase(), at: now }
      const hit = enabled.find(i => actions[i].label.toLowerCase().startsWith(typed.current.text))
      if (hit != null) setActive(hit)
    }
  }

  const button = (
    <button
      ref={triggerRef}
      type="button"
      aria-haspopup="menu"
      aria-expanded={open}
      aria-controls={open ? menuId : undefined}
      aria-label={label}
      onClick={() => (open ? close() : openMenu('first'))}
      onKeyDown={onTriggerKey}
      className={trigger
        ? className
        : `inline-flex h-9 w-9 items-center justify-center rounded-[10px] text-muted hover:text-body hover:bg-surface-hover transition-colors aria-expanded:bg-surface-hover aria-expanded:text-body ${className}`}
    >
      {trigger ?? <Icon.More className="h-4 w-4" />}
    </button>
  )

  let actionIndex = -1
  return (
    <>
      <Tooltip content={label} disabled={!tooltip || !!trigger || open}>{button}</Tooltip>
      {open && (
        <FloatingLayer
          anchor={anchor}
          layerRef={layerRef}
          side="bottom"
          align={align}
          offset={4}
          id={menuId}
          role="menu"
          aria-label={label}
          onKeyDown={onMenuKey}
          className="min-w-[184px] max-w-[min(20rem,calc(100vw-16px))] overflow-y-auto rounded-xl border border-default bg-surface p-1.5 shadow-lg"
        >
          {items.map(entry => {
            if ('separator' in entry) return <div key={entry.key} role="separator" className="my-1 h-px bg-default" />
            actionIndex++
            const index = actionIndex
            return (
              <button
                key={entry.key}
                type="button"
                role="menuitem"
                tabIndex={active === index ? 0 : -1}
                aria-disabled={entry.disabled || undefined}
                onClick={() => choose(entry)}
                onMouseMove={() => { if (!entry.disabled && active !== index) setActive(index) }}
                className={`flex h-9 w-full items-center gap-2.5 rounded-lg px-2.5 text-left text-[13px] transition-colors
                  ${entry.disabled ? 'opacity-50 cursor-not-allowed' : 'cursor-pointer'}
                  ${entry.tone === 'danger' ? 'text-danger hover:bg-danger-bg focus-visible:bg-danger-bg' : 'text-body-2 hover:bg-surface-hover focus-visible:bg-surface-hover focus-visible:text-body'}`}
              >
                {entry.icon && <span aria-hidden="true" className="flex w-4 shrink-0 items-center justify-center">{entry.icon}</span>}
                <span className="min-w-0 flex-1 truncate">{entry.label}</span>
                {entry.hint && <span className="shrink-0 text-[11px] text-faint">{entry.hint}</span>}
              </button>
            )
          })}
        </FloatingLayer>
      )}
    </>
  )
}
