import { useEffect, useState, type FormEvent, type ReactNode } from 'react'
import { PageTitle } from '../components/PageShell'
import PasswordInput from '../components/PasswordInput'
import { QrCode } from '../components/QrCode'
import { SettingsFooter, SettingsPanel, SettingsSection } from '../components/settings/SettingsPanel'
import { Alert, Badge, Button, Card, Field, Icon, Input, LoadingBlock } from '../components/ui'
import { ApiError, errorText } from '../lib/api'
import { passwordAuth } from '../lib/passwordAuth'
import { ROLE_LABEL } from '../lib/roles'
import type { Me } from '../lib/types'

interface Security { username: string; twoFactorEnabled: boolean; recoveryCodesRemaining: number }
type Action = 'enroll' | 'disable' | 'recovery' | null

const POLICY_NOTE = 'Your administrator requires two-factor authentication, so it can’t be turned off.'

function CodeField({ value, onChange, recovery = false }: { value: string; onChange: (v: string) => void; recovery?: boolean }) {
  return <Field label={recovery ? 'Authenticator or recovery code' : 'Authentication code'}>{id => <Input id={id} name="code" autoComplete="one-time-code" autoCapitalize="none" spellCheck={false} inputMode={recovery ? 'text' : 'numeric'} required pattern={recovery ? undefined : '[0-9]{6}'} maxLength={recovery ? 64 : 6} value={value} onChange={e => onChange(e.target.value)} className="font-mono" />}</Field>
}

/** A responsive two-column grid: one column on phones, two from the `sm` breakpoint. */
function Grid({ children }: { children: ReactNode }) {
  return <div className="grid grid-cols-1 items-start gap-x-5 gap-y-4 sm:grid-cols-2">{children}</div>
}

export default function AccountSecurity({ user, onMeChanged }: { user: Me; onMeChanged?: () => Promise<unknown> }) {
  const [security, setSecurity] = useState<Security | null>(null)
  const [loadError, setLoadError] = useState('')
  const [nonce, setNonce] = useState(0)
  const [action, setAction] = useState<Action>(null)
  const [password, setPassword] = useState('')
  const [code, setCode] = useState('')
  const [secret, setSecret] = useState('')
  const [otpauthUrl, setOtpauthUrl] = useState('')
  const [codes, setCodes] = useState<string[]>([])
  const [savedCodes, setSavedCodes] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [policyLocked, setPolicyLocked] = useState(false)
  const [currentPassword, setCurrentPassword] = useState('')
  const [newPassword, setNewPassword] = useState('')
  const [confirm, setConfirm] = useState('')
  const [passwordCode, setPasswordCode] = useState('')
  const [passwordBusy, setPasswordBusy] = useState(false)
  const [passwordError, setPasswordError] = useState('')
  const [passwordNotice, setPasswordNotice] = useState('')

  useEffect(() => {
    let active = true
    setLoadError('')
    passwordAuth<Security>('/api/auth/security').then(data => { if (active) setSecurity(data) }).catch(err => { if (active) setLoadError(errorText(err, 'Could not load account security.')) })
    return () => { active = false }
  }, [nonce])

  function start(next: Action) {
    setAction(next); setPassword(''); setCode(''); setSecret(''); setError(''); setNotice('')
  }
  async function updateTwoFactor(event: FormEvent) {
    event.preventDefault()
    if (busy || !action) return
    setBusy(true); setError(''); setNotice('')
    try {
      if (action === 'enroll' && !secret) {
        const data = await passwordAuth<{ secret: string; otpauthUrl: string }>('/api/auth/2fa/enroll', { password })
        setSecret(data.secret); setOtpauthUrl(data.otpauthUrl ?? ''); setPassword(''); setCode('')
      } else if (action === 'enroll' || action === 'recovery') {
        const data = await passwordAuth<{ recoveryCodes: string[] }>(action === 'enroll' ? '/api/auth/2fa/enable' : '/api/auth/2fa/recovery', action === 'enroll' ? { code: code.trim() } : { password, code: code.trim() })
        setCodes(data.recoveryCodes); setSavedCodes(false); setAction(null); setSecret(''); setOtpauthUrl(''); setPassword(''); setCode(''); setNonce(n => n + 1)
      } else {
        await passwordAuth('/api/auth/2fa/disable', { password, code: code.trim() })
        start(null); setNotice('Two-factor authentication is now disabled.'); setNonce(n => n + 1)
        void onMeChanged?.()
      }
    } catch (err) {
      if (action === 'disable' && err instanceof ApiError && err.status === 409) { start(null); setPolicyLocked(true) }
      else setError(errorText(err, 'The security change could not finish.'))
    }
    finally { setBusy(false) }
  }
  // Enabling is the last step of the enforced set-up. The account is refreshed only now, so the recovery codes
  // stay on screen until they are saved; the app shell then opens on the overview by itself.
  async function finishCodes() {
    setCodes([]); setNotice('Two-factor authentication is enabled. Your recovery codes are ready to use.')
    await onMeChanged?.()
  }
  async function changePassword(event: FormEvent) {
    event.preventDefault()
    if (passwordBusy) return
    setPasswordError(''); setPasswordNotice('')
    if (newPassword !== confirm) { setPasswordError('The new passwords do not match.'); return }
    setPasswordBusy(true)
    try {
      await passwordAuth('/api/auth/password', { currentPassword, newPassword, ...(security?.twoFactorEnabled ? { code: passwordCode.trim() } : {}) })
      setCurrentPassword(''); setNewPassword(''); setConfirm(''); setPasswordCode(''); setPasswordNotice('Your password has been changed.')
    } catch (err) { setPasswordError(errorText(err, 'Could not change your password.')) }
    finally { setPasswordBusy(false) }
  }
  function downloadCodes() {
    const blob = new Blob([`Opskest recovery codes for ${security?.username ?? 'your account'}\nKeep these codes private. Each code can be used once.\n\n${codes.join('\n')}\n`], { type: 'text/plain;charset=utf-8' })
    const url = URL.createObjectURL(blob)
    const link = document.createElement('a'); link.href = url; link.download = 'opskest-recovery-codes.txt'; link.click()
    setTimeout(() => URL.revokeObjectURL(url), 1000)
  }

  const enabled = security?.twoFactorEnabled ?? user.twoFactorEnabled
  const username = security?.username ?? user.username ?? user.email
  const displayName = user.name || username
  const remaining = security?.recoveryCodesRemaining ?? 0
  const formTitle = action === 'disable' ? 'Disable two-factor authentication' : action === 'recovery' ? 'Regenerate recovery codes' : secret ? 'Connect your authenticator' : 'Confirm your password'

  return <div className="mx-auto w-full max-w-3xl">
    <PageTitle title="Account security">Manage how you sign in and protect your account.</PageTitle>
    <div className="space-y-6">
      {user.twoFactorRequired && <Alert tone="accent">Your administrator requires two-factor authentication. Set it up to continue.</Alert>}
      {loadError && <Card className="space-y-3 p-4"><p role="alert" className="text-sm text-danger">{loadError}</p><div className="flex items-center gap-3"><Button onClick={() => setNonce(n => n + 1)}>Try again</Button><a href="/login" className="text-sm text-accent hover:underline">Sign in again</a></div></Card>}

      <Card className="flex flex-wrap items-center gap-x-4 gap-y-4 p-5">
        <div className="flex h-12 w-12 shrink-0 items-center justify-center rounded-full bg-accent-subtle text-lg font-semibold text-accent" aria-hidden="true">{displayName.slice(0, 1).toUpperCase()}</div>
        <div className="min-w-0 flex-1 basis-48">
          <h2 className="truncate text-base font-semibold leading-6 text-body">{displayName}</h2>
          <p className="truncate font-mono text-[13px] leading-5 text-muted">{username}</p>
        </div>
        <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
          <Badge>{ROLE_LABEL[user.role] ?? 'Viewer'}</Badge>
          {enabled !== undefined && <p className="flex items-center gap-2 text-[13px] text-muted">Two-factor:<Badge tone={enabled ? 'ok' : 'neutral'}>{enabled ? 'On' : 'Off'}</Badge></p>}
        </div>
      </Card>

      {!security ? !loadError && <LoadingBlock label="Loading account security" /> : <>
        <SettingsPanel id="two-factor" title="Two-factor authentication" titleExtra={<Badge tone={security.twoFactorEnabled ? 'ok' : 'neutral'}>{security.twoFactorEnabled ? 'Enabled' : 'Not enabled'}</Badge>}
          description="Ask for a code from an authenticator app, as well as your password, when you sign in.">
          {codes.length > 0 ? (
            <SettingsSection>
              <div className="space-y-4 rounded-[14px] border border-accent-border bg-accent-subtle p-4">
                <h3 className="text-sm font-semibold">Save your recovery codes</h3>
                <p className="text-sm text-body-2">Store these somewhere safe, such as your password manager. Each code works once if you lose access to your authenticator. This is the only time these codes are shown.</p>
                <ul aria-label="Recovery codes" className="grid select-all grid-cols-1 gap-2 font-mono text-sm sm:grid-cols-2">{codes.map(c => <li key={c}>{c}</li>)}</ul>
                <Button onClick={downloadCodes}>Download codes</Button>
                <label className="flex items-start gap-2 text-sm"><input type="checkbox" checked={savedCodes} onChange={e => setSavedCodes(e.target.checked)} className="mt-1 accent-accent" />I have saved these recovery codes.</label>
                <Button variant="primary" disabled={!savedCodes} onClick={finishCodes}>Done</Button>
              </div>
            </SettingsSection>
          ) : <>
            <SettingsSection title="Authenticator app"
              description={security.twoFactorEnabled ? <>Signing in as <strong className="font-medium text-body">{security.username}</strong> asks for a code from your authenticator app after your password.</> : <>Use any authenticator app that makes time-based codes. You will enter a six-digit code each time you sign in as <strong className="font-medium text-body">{security.username}</strong>.</>}
              actions={!action ? (security.twoFactorEnabled
                ? <Button disabled={policyLocked} title={policyLocked ? POLICY_NOTE : undefined} onClick={() => start('disable')}>Disable two-factor authentication</Button>
                : <Button variant="primary" onClick={() => start('enroll')}>Enable two-factor authentication</Button>) : undefined}>
              {policyLocked || notice ? <>
                {policyLocked && <p className="text-xs text-muted">{POLICY_NOTE}</p>}
                {notice && <p role="status" className="text-sm text-ok">{notice}</p>}
              </> : null}
            </SettingsSection>
            {security.twoFactorEnabled && (
              <SettingsSection title="Recovery codes"
                description={<><strong className="font-medium text-body">{remaining} recovery {remaining === 1 ? 'code' : 'codes'} left.</strong> Each works once if you lose access to your authenticator.</>}
                actions={!action ? <Button onClick={() => start('recovery')}>Regenerate</Button> : undefined} />
            )}
            {action && (
              <SettingsSection title={formTitle}>
                <form onSubmit={updateTwoFactor} className="space-y-4" aria-busy={busy}>
                  {action === 'disable' && <p className="text-sm text-muted">Your account will only require a password to sign in. Existing recovery codes will stop working.</p>}
                  {action === 'recovery' && <p className="text-sm text-muted">All previous recovery codes will stop working when you create a new set.</p>}
                  {secret ? <>
                    <div className="flex flex-col gap-5 sm:flex-row sm:items-start">
                      {otpauthUrl && <QrCode value={otpauthUrl} label="QR code for your authenticator app" />}
                      <ol className="list-decimal space-y-2 pl-5 text-sm text-muted">
                        <li>In your authenticator app, add an account and scan the QR code{otpauthUrl ? '' : ' (or choose to add one using a setup key)'}. If you cannot scan it, choose to add an account using the setup key below.</li>
                        <li>For a setup key, use <strong className="font-medium text-body">Opskest: {security.username}</strong> as the account name and choose <strong className="font-medium text-body">Time based</strong>.</li>
                        <li>Enter the six-digit code your app shows below.</li>
                      </ol>
                    </div>
                    <Grid>
                      <Field label="Setup key">{id => <Input id={id} value={secret} readOnly spellCheck={false} className="w-full font-mono text-xs" onFocus={e => e.target.select()} />}</Field>
                      <CodeField value={code} onChange={setCode} />
                    </Grid>
                  </> : action === 'enroll'
                    ? <Field label="Current password" className="sm:max-w-[calc(50%-10px)]">{id => <PasswordInput id={id} name="password" autoComplete="current-password" required maxLength={128} value={password} onChange={e => setPassword(e.target.value)} />}</Field>
                    : <Grid>
                      <Field label="Current password">{id => <PasswordInput id={id} name="password" autoComplete="current-password" required maxLength={128} value={password} onChange={e => setPassword(e.target.value)} />}</Field>
                      <CodeField value={code} onChange={setCode} recovery />
                    </Grid>}
                  {error && <p role="alert" className="text-sm text-danger">{error}</p>}
                  <SettingsFooter>
                    <Button disabled={busy} onClick={() => start(null)}>Cancel</Button>
                    <Button type="submit" variant={action === 'disable' ? 'danger' : 'primary'} busy={busy}>{action === 'disable' ? 'Disable two-factor authentication' : action === 'recovery' ? 'Create new recovery codes' : secret ? 'Verify and enable' : 'Continue'}</Button>
                  </SettingsFooter>
                </form>
              </SettingsSection>
            )}
          </>}
        </SettingsPanel>

        <SettingsPanel id="password" title="Change password" description="Choose a new password for your account.">
          <SettingsSection>
            <form onSubmit={changePassword} aria-busy={passwordBusy}>
              <input type="text" autoComplete="username" value={security.username} readOnly hidden />
              <Grid>
                <Field label="Current password" className={security.twoFactorEnabled ? '' : 'sm:col-span-2'}>{id => <PasswordInput id={id} name="current-password" autoComplete="current-password" required maxLength={128} value={currentPassword} onChange={e => setCurrentPassword(e.target.value)} />}</Field>
                {security.twoFactorEnabled && <CodeField value={passwordCode} onChange={setPasswordCode} recovery />}
                <Field label="New password" hint={<span id="new-password-hint">Use 12–128 characters and a password you don’t use elsewhere.</span>}>{id => <PasswordInput id={id} name="new-password" autoComplete="new-password" required minLength={12} maxLength={128} value={newPassword} onChange={e => setNewPassword(e.target.value)} aria-describedby="new-password-hint" />}</Field>
                <Field label="Confirm new password">{id => <PasswordInput id={id} name="confirm-password" autoComplete="new-password" required minLength={12} maxLength={128} value={confirm} onChange={e => setConfirm(e.target.value)} />}</Field>
              </Grid>
              {passwordError && <p role="alert" className="mt-4 text-sm text-danger">{passwordError}</p>}
              <SettingsFooter status={passwordNotice ? <span className="flex items-center gap-1.5 font-semibold text-ok"><Icon.Check className="h-4 w-4" />{passwordNotice}</span> : undefined}>
                <Button type="submit" busy={passwordBusy} disabled={busy || codes.length > 0} variant="primary">Update password</Button>
              </SettingsFooter>
            </form>
          </SettingsSection>
        </SettingsPanel>
      </>}
    </div>
  </div>
}
