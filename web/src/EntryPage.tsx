import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { NotFoundError, getEntry } from './api.ts'
import { EntryEditor } from './EntryEditor.tsx'

export function EntryPage({ entryId }: { entryId: string }) {
  // Checked over REST first so an unknown id gets a proper "not found" page
  // instead of a websocket that opens and immediately closes.
  const entry = useQuery({
    queryKey: ['entry', entryId],
    queryFn: () => getEntry(entryId),
    retry: (count, err) => !(err instanceof NotFoundError) && count < 3,
  })

  return (
    <main className="wide">
      <p>
        <Link to="/">← All entries</Link>
      </p>
      {entry.isPending && <p>Loading…</p>}
      {entry.error instanceof NotFoundError && (
        <p role="alert">This entry does not exist.</p>
      )}
      {entry.isError && !(entry.error instanceof NotFoundError) && (
        <p role="alert">Could not load this entry.</p>
      )}
      {entry.isSuccess && <EntryEditor entryId={entryId} />}
    </main>
  )
}
