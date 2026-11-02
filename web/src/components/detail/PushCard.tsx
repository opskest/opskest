// Push monitors: the URL a job calls, and a ready-to-paste curl example.

import { Button, useToast } from '../ui'

export function CopyRow({ text, label }: { text: string; label: string }) {
  const { push } = useToast()
  async function copy() {
    try {
      await navigator.clipboard.writeText(text)
      push(`${label} copied.`, 'ok')
    } catch {
      push('Could not copy — select the text and copy it manually.', 'danger')
    }
  }
  return (
    <div className="flex items-center gap-2 min-w-0">
      <code className="flex-1 min-w-0 truncate text-[12px] bg-surface-2 border border-default rounded-md px-2.5 py-1.5 font-mono text-body-2" title={text}>
        {text}
      </code>
      <Button size="sm" onClick={copy} aria-label={`Copy ${label}`}>Copy</Button>
    </div>
  )
}

export function PushCard({ url }: { url: string }) {
  const curl = `curl -fsS "${url}?status=up&msg=OK&ms=123"`
  return (
    <section className="bg-surface border border-default rounded-[14px] p-4 flex flex-col gap-2.5">
      <div className="flex flex-col gap-0.5">
        <h2 className="text-base font-semibold text-body">Push URL</h2>
        <p className="text-[12px] text-muted">
          Have your job call this every interval (GET or POST). Add <code className="font-mono">status=down</code> to report a failure;
          if no push arrives within the interval plus the grace period, the monitor goes down.
        </p>
      </div>
      <CopyRow text={url} label="Push URL" />
      <CopyRow text={curl} label="curl example" />
    </section>
  )
}
