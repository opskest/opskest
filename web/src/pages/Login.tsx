import { useEffect, useState, type FormEvent } from 'react'
import Brand from '../components/Brand'
import KestrelMark from '../components/KestrelMark'
import PasswordInput from '../components/PasswordInput'
import { ThemeToggle } from '../components/PageShell'
import { Button, Field, Input, LoadingBlock } from '../components/ui'
import { errorText } from '../lib/api'
import { passwordAuth } from '../lib/passwordAuth'

/** Keep post-login navigation on this origin, including backslash-normalized URLs. */
function nextPath(value: string | null): string {
  if (!value || !value.startsWith('/') || value.startsWith('//') || value.includes('\\')) return '/'
  return value
}

export default function Login() {
  const [setup, setSetup] = useState<boolean | null>(null)
  const [loadError, setLoadError] = useState('')
  const [username, setUsername] = useState('')
  const [name, setName] = useState('')
  const [password, setPassword] = useState('')
  const [confirm, setConfirm] = useState('')
  const [code, setCode] = useState('')
  const [twoFactor, setTwoFactor] = useState(false)
  const [recovery, setRecovery] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [attempt, setAttempt] = useState(0)
  useEffect(() => {
    let active = true
    setLoadError('')
    passwordAuth<{ setupRequired: boolean }>('/api/auth/status').then(data => { if (active) setSetup(data.setupRequired) }).catch(err => { if (active) setLoadError(errorText(err, 'Could not load sign-in options.')) })
    return () => { active = false }
  }, [attempt])

  async function submit(event: FormEvent) {
    event.preventDefault()
    if (busy || setup == null) return
    setError('')
    if (setup && password !== confirm) { setError('The passwords do not match.'); return }
    setBusy(true)
    try {
      const response = await passwordAuth<{ ok?: boolean; requiresTwoFactor?: boolean }>(setup ? '/api/auth/setup' : '/api/auth/login', setup ? { username: username.trim(), name: name.trim(), password } : { username: username.trim(), password, ...(twoFactor ? { code: code.trim() } : {}) })
      if (response.requiresTwoFactor) { setTwoFactor(true); setCode(''); return }
      if (!response.ok) throw new Error('Sign-in could not finish. Please try again.')
      setPassword(''); setConfirm(''); setCode('')
      window.location.assign(nextPath(new URLSearchParams(window.location.search).get('next')))
    } catch (err) { setError(errorText(err, 'Could not sign in. Please try again.')) }
    finally { setBusy(false) }
  }

  return <div className="min-h-screen bg-ground grid lg:grid-cols-2">
    <aside className="relative hidden lg:flex flex-col justify-between border-r border-default bg-surface p-12 min-h-screen"><Brand /><div className="max-w-md py-20"><KestrelMark className="w-24 h-24 text-accent mb-10" /><h2 className="text-4xl leading-tight font-semibold tracking-tight">A clear view of every service.</h2><p className="text-body-2 text-base leading-relaxed mt-6">Monitor your infrastructure, coordinate incident response, and keep customers informed from one workspace.</p></div><p className="text-sm text-muted">Opskest · Self-hosted monitoring</p></aside><div className="relative flex min-h-screen items-center justify-center px-6 py-24"><header className="absolute inset-x-0 top-0 flex items-center justify-between p-6"><span className="lg:invisible"><Brand /></span><ThemeToggle /></header>
    <main className="relative w-full max-w-[420px]">
      <div className="mb-9">
        
        <h1 className="text-[27px] leading-tight font-semibold text-body tracking-tight">{setup ? 'Create your workspace' : twoFactor ? 'Verify your identity' : 'Sign in'}</h1>
        <p className="text-muted text-sm mt-2">{setup ? 'Create the administrator account for your Opskest workspace.' : twoFactor ? 'Verify it’s you to open your workspace.' : 'Sign in to your Opskest workspace.'}</p>
      </div>
      {loadError ? <div className="space-y-5"><p role="alert" className="text-sm text-danger">{loadError}</p><Button onClick={() => setAttempt(n => n + 1)}>Try again</Button></div> : setup == null ? <LoadingBlock label="Loading sign-in options" /> :
        <form onSubmit={submit} className="bg-surface border border-default rounded-[14px] p-6 space-y-4" aria-busy={busy}>
          {!twoFactor ? <>
            {setup && <Field label="Your name">{id => <Input id={id} name="name" autoComplete="name" value={name} onChange={e => setName(e.target.value)} required maxLength={120} />}</Field>}
            <Field label="Username">{id => <Input id={id} name="username" autoComplete="username" autoCapitalize="none" spellCheck={false} required minLength={setup ? 3 : undefined} maxLength={32} pattern={setup ? '[A-Za-z0-9_.\\-]{3,32}' : undefined} title={setup ? '3–32 letters, numbers, dots, underscores, or hyphens' : undefined} value={username} onChange={e => setUsername(e.target.value)} />}</Field>
            <Field label="Password">{id => <PasswordInput id={id} name="password" autoComplete={setup ? 'new-password' : 'current-password'} required minLength={setup ? 12 : undefined} maxLength={128} value={password} onChange={e => setPassword(e.target.value)} aria-describedby={setup ? 'password-hint' : undefined} />}</Field>
            {setup && <><p id="password-hint" className="text-xs text-muted">Use at least 12 characters. A unique passphrase works well.</p><Field label="Confirm password">{id => <PasswordInput id={id} name="confirm-password" autoComplete="new-password" required minLength={12} maxLength={128} value={confirm} onChange={e => setConfirm(e.target.value)} />}</Field></>}
          </> : <>
            <p className="text-sm text-muted">{recovery ? 'Enter one of the recovery codes you saved when enabling two-factor authentication.' : 'Enter the six-digit code from your authenticator app.'}</p>
            <Field label={recovery ? 'Recovery code' : 'Authentication code'}>{id => <Input key={recovery ? 'recovery' : 'totp'} id={id} name="code" autoFocus required autoComplete="one-time-code" inputMode={recovery ? 'text' : 'numeric'} pattern={recovery ? undefined : '[0-9]{6}'} maxLength={recovery ? 64 : 6} value={code} onChange={e => setCode(e.target.value)} className="font-mono tracking-widest" />}</Field>
            <button type="button" className="text-xs text-accent hover:underline" onClick={() => { setRecovery(v => !v); setCode(''); setError('') }}>{recovery ? 'Use an authenticator code' : 'Use a recovery code'}</button>
          </>}
          {error && <p role="alert" className="text-sm text-danger">{error}</p>}
          <Button type="submit" variant="primary" busy={busy} className="w-full">{setup ? 'Create workspace' : twoFactor ? 'Verify and sign in' : 'Sign in'}</Button>
          {twoFactor && <Button className="w-full" disabled={busy} onClick={() => { setTwoFactor(false); setCode(''); setPassword(''); setError('') }}>Back to sign in</Button>}
          {setup && <p className="text-xs text-muted leading-relaxed">This account can manage monitors, workspace settings, and team access. You can enable two-factor authentication after signing in.</p>}
        </form>}
      <p className="text-xs text-faint mt-8 border-t border-default pt-5">Your workspace is managed by your team.</p>
    </main></div>
  </div>
}
