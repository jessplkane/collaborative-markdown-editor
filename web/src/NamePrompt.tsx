import { useState } from 'react'
import { MAX_NAME_LENGTH } from './identity.ts'

/** Asks what other viewers should call this user. Shown once per browser. */
export function NamePrompt({ onSubmit }: { onSubmit: (name: string) => void }) {
  const [value, setValue] = useState('')
  const name = value.trim()

  return (
    <form
      className="name-prompt"
      onSubmit={(e) => {
        e.preventDefault()
        if (name) onSubmit(name)
      }}
    >
      <label>
        Your name
        <input
          value={value}
          onChange={(e) => setValue(e.target.value)}
          maxLength={MAX_NAME_LENGTH}
          autoFocus
          required
        />
      </label>
      <button type="submit" disabled={!name}>
        Join
      </button>
    </form>
  )
}
