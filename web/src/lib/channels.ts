// Alert channel types and the names people know them by. One list so the
// settings page, the monitor form and the provider dialog never disagree.

import type { ChannelType } from './types'

export const CHANNEL_LABELS: Record<ChannelType, { label: string }> = {
  slack: { label: 'Slack Webhook' },
  email: { label: 'Email (SMTP)' },
  teams: { label: 'Microsoft Teams' },
  webhook: { label: 'Custom Webhook' },
  pagerduty: { label: 'PagerDuty v2' },
  gchat: { label: 'Google Chat' },
  opsgenie: { label: 'Opsgenie' },
  discord: { label: 'Discord' },
  telegram: { label: 'Telegram' },
  ntfy: { label: 'ntfy' },
  gotify: { label: 'Gotify' },
  pushover: { label: 'Pushover' },
  matrix: { label: 'Matrix' },
  mattermost: { label: 'Mattermost' },
  rocketchat: { label: 'Rocket.Chat' },
  zulip: { label: 'Zulip' },
  webex: { label: 'Webex' },
  pushbullet: { label: 'Pushbullet' },
  ifttt: { label: 'IFTTT' },
  apprise: { label: 'Apprise API' },
  twilio: { label: 'Twilio SMS' },
  homeassistant: { label: 'Home Assistant' },
  bark: { label: 'Bark' },
  signal: { label: 'Signal' },
}

/** The display name of a channel type; an unknown type is shown as is. */
export function channelLabel(type: string): string {
  return (CHANNEL_LABELS as Record<string, { label: string } | undefined>)[type]?.label ?? type
}
