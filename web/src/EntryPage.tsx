import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useNavigate } from '@tanstack/react-router'
import { useState } from 'react'
import { NotFoundError, getEntry } from './api.ts'
import { EntryEditor } from './EntryEditor.tsx'
import { getName, setName } from './identity.ts'
import { NamePrompt } from './NamePrompt.tsx'

export function EntryPage({ entryId }: { entryId: string }) {
  // Checked over REST first so an unknown id gets a proper "not found" page
  // instead of a websocket that opens and immediately closes.
  const entry = useQuery({
    queryKey: ['entry', entryId],
    queryFn: () => getEntry(entryId),
    retry: (count, err) => !(err instanceof NotFoundError) && count < 3,
  })
  const [name, setChosenName] = useState(getName)
  const queryClient = useQueryClient()
  const navigate = useNavigate()

  return (
    <main className="wide">
      <p>
        <Link to="/">← All entries</Link>
      </p>
      {/* Once the entry has been seen to exist, a failed re-check (the query
          refetches when the tab regains focus) must not take the editor away:
          it holds edits the server has not confirmed. */}
      {!entry.data && entry.isPending && <p>Loading…</p>}
      {!entry.data && entry.error instanceof NotFoundError && (
        <p role="alert">This entry does not exist.</p>
      )}
      {!entry.data &&
        entry.isError &&
        !(entry.error instanceof NotFoundError) && (
          <p role="alert">Could not load this entry.</p>
        )}
      {entry.data &&
        (name ? (
          <EntryEditor
            entryId={entryId}
            name={name}
            onDeleted={() => {
              // Otherwise the page would come back from cache if revisited.
              queryClient.removeQueries({ queryKey: ['entry', entryId] })
              void navigate({ to: '/', search: { notice: 'deleted' } })
            }}
          />
        ) : (
          <NamePrompt
            onSubmit={(chosen) => {
              setName(chosen)
              setChosenName(chosen)
            }}
          />
        ))}
    </main>
  )
}
