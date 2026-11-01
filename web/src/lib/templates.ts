// Saved monitor templates: the monitor form's settings, kept by the server
// without a name, target, project or secrets.

import { draftFromOut, toMonitorIn, type Draft } from './monitorForm'
import type { MonitorIn, MonitorOut, MonitorType } from './types'

export type TemplateConfig = Partial<MonitorIn> & { type: MonitorType }

export interface MonitorTemplate {
  id: number
  name: string
  description: string
  type: MonitorType
  config: TemplateConfig
  createdAt: number
  updatedAt: number
}

/** What gets saved: the draft as the API would receive it. The server strips identity, target, project and secrets. */
export function templateConfig(d: Draft): TemplateConfig {
  const { name: _name, target: _target, groupId: _groupId, secrets: _secrets, ...rest } = toMonitorIn(d)
  return { ...rest, type: d.type }
}

/** The draft with the template's settings applied; name, target, project and secrets stay as typed. */
export function applyTemplate(d: Draft, t: MonitorTemplate): Draft {
  const c = t.config
  const fromTemplate = draftFromOut({
    ...c,
    id: 0,
    name: d.name,
    target: d.target,
    groupId: d.groupId,
    headers: (c.headers ?? []).map(h => ({ name: h.name, value: h.value ?? '', secret: false, isSet: false })),
    secrets: undefined,
  } as unknown as MonitorOut)
  return {
    ...fromTemplate,
    name: d.name,
    target: d.target,
    groupId: d.groupId,
    basicPass: d.basicPass,
    bearerToken: d.bearerToken,
    proxyPass: d.proxyPass,
    clientCert: d.clientCert,
    clientKey: d.clientKey,
    oauthSecret: d.oauthSecret,
    channelIds: c.channelIds ?? d.channelIds,
  }
}
