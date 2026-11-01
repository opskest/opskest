import { useState, type InputHTMLAttributes } from 'react'
import { Input } from './ui'

export default function PasswordInput({ className = '', ...props }: Omit<InputHTMLAttributes<HTMLInputElement>, 'type'>) {
  const [visible, setVisible] = useState(false)
  return <div className="relative">
    <Input {...props} type={visible ? 'text' : 'password'} className={`w-full pr-16 ${className}`} />
    <button type="button" aria-label={visible ? 'Hide password' : 'Show password'} aria-pressed={visible} onClick={() => setVisible(v => !v)} className="absolute right-1 top-1 h-7 px-2 text-xs text-muted rounded-lg hover:text-body focus-visible:outline focus-visible:outline-2 focus-visible:outline-accent">{visible ? 'Hide' : 'Show'}</button>
  </div>
}
