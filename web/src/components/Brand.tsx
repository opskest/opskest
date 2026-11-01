import { Link } from 'wouter'
import KestrelMark from './KestrelMark'
export default function Brand({ href, size = 'md' }: { href?: string; size?: 'md' | 'lg' }) {
  const inner = <><KestrelMark className={size === 'lg' ? 'w-10 h-10' : 'w-8 h-8'} /><span className={size === 'lg' ? 'text-2xl font-semibold tracking-tight' : 'text-xl font-semibold tracking-tight'}>Opskest</span></>
  const cls = 'inline-flex items-center gap-2 shrink-0 rounded-sm text-body'
  return href ? <Link href={href} className={cls + ' hover:opacity-80 transition-opacity'} aria-label="Opskest home">{inner}</Link> : <span className={cls}>{inner}</span>
}
