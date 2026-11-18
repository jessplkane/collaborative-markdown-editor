import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { NotFoundError, getEntry } from './api.ts'

export function EntryPage({ entryId }: { entryId: string }) {
  const entry = useQuery({
    queryKey: ['entry', entryId],
    queryFn: () => getEntry(entryId),
    retry: (count, err) => !(err instanceof NotFoundError) && count < 3,
  })

  return (
    <main>
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
      {entry.isSuccess && (
        <>
          <h1>{entry.data.title}</h1>
          <p>The editor arrives in the next slice.</p>
        </>
      )}
    </main>
  )
}
