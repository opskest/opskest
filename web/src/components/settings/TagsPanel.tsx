import { useEffect, useMemo, useState } from 'react'
import { api, unwrapList } from '../../lib/api'
import { useLive } from '../../lib/live'
import { normTag } from '../../lib/normalize'
import type { Tag } from '../../lib/types'
import { TagManager } from '../TagManager'
import { Skeleton } from '../ui'
import { SettingsSection } from './SettingsPanel'

// Settings → Tags: every tag in the workspace, with how many monitors use it.
export function TagsPanel() {
  const live = useLive()
  const [tags, setTags] = useState<Tag[] | null>(null)
  const [failed, setFailed] = useState(false)
  useEffect(() => {
    api.get<unknown>('/api/tags').then(d => setTags(unwrapList(d, 'tags').map(normTag))).catch(() => setFailed(true))
  }, [])
  const usage = useMemo(() => {
    const counts: Record<number, number> = {}
    for (const m of live.monitors.values()) for (const id of m.tagIds ?? []) counts[id] = (counts[id] ?? 0) + 1
    return counts
  }, [live.monitors])

  return (
    <SettingsSection title="Tags" description="Labels you attach to monitors, so they are easy to find and group. Anyone who can edit monitors can also pick tags in the monitor form.">
      {failed ? <p className="text-[13px] text-danger">Could not load the tags. Reload to try again.</p>
        : tags === null ? <Skeleton className="h-24 w-full" />
        : <TagManager tags={tags} usage={usage} onChange={setTags} />}
    </SettingsSection>
  )
}
