import type { Config } from 'tailwindcss'

// Tokens live in src/index.css as CSS custom properties; this exposes them
// as Tailwind utilities (bg-surface, text-muted, border-default, …) so
// components never hardcode a colour. Ported from logs-hub's config.
//
// Each token is a function so opacity modifiers work on it too
// (`bg-danger/10`, `border-warn/30`): with a modifier it becomes a
// color-mix() of the token and transparent; without one it is the plain
// var(), so the common case costs nothing.
function token(name: string) {
  return ({ opacityValue }: { opacityValue?: string }) =>
    opacityValue === undefined || opacityValue.startsWith('var(') || opacityValue === '1'
      ? `var(${name})`
      : `color-mix(in srgb, var(${name}) calc(${opacityValue} * 100%), transparent)`
}

export default {
  content: ['./index.html', './src/**/*.{ts,tsx}'],
  // `dark:` follows what the viewer actually sees: an explicit .dark stamp,
  // or the OS preference while the theme is 'system' (no .light stamp).
  darkMode: ['variant', ['&:is(.dark *)', '@media (prefers-color-scheme: dark) { &:not(.light *) }']],
  theme: {
    extend: {
      colors: {
        ground: token('--ground'),
        surface: token('--surface'),
        'surface-2': token('--surface-2'),
        'surface-hover': token('--surface-hover'),
        default: token('--border'),
        strong: token('--border-strong'),

        body: token('--text'),
        'body-2': token('--text-2'),
        muted: token('--muted'),
        faint: token('--faint'),

        accent: {
          DEFAULT: token('--accent'),
          hover: token('--accent-hover'),
          fg: token('--accent-fg'),
          subtle: token('--accent-subtle'),
          border: token('--accent-border'),
        },

        ok: token('--ok'),
        'ok-bg': token('--ok-bg'),
        warn: token('--warn'),
        'warn-bg': token('--warn-bg'),
        danger: token('--danger'),
        'danger-bg': token('--danger-bg'),
      },
      borderColor: { DEFAULT: 'var(--border)' },
      borderRadius: { DEFAULT: 'var(--radius)', sm: 'var(--radius-sm)', md: '7px', lg: '10px', xl: '12px', '2xl': '14px' },
      fontFamily: {
        sans: ['Segoe UI', 'ui-sans-serif', 'system-ui', '-apple-system', 'sans-serif'],
        mono: ['JetBrains Mono', 'ui-monospace', 'SF Mono', 'Menlo', 'Consolas', 'monospace'],
      },
      keyframes: {
        'modal-enter': {
          from: { opacity: '0', transform: 'scale(0.96) translateY(4px)' },
          to: { opacity: '1', transform: 'scale(1) translateY(0)' },
        },
        'fade-in': {
          from: { opacity: '0' },
          to: { opacity: '1' },
        },
      },
      animation: {
        'modal-enter': 'modal-enter 150ms cubic-bezier(0.16, 1, 0.3, 1) forwards',
        'fade-in': 'fade-in 150ms ease-out forwards',
      },
    },
  },
  plugins: [],
} satisfies Config
