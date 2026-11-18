import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useNavigate } from '@tanstack/react-router'
import { createEntry, deleteEntry, listEntries } from './api.ts'

const entriesKey = ['entries'] as const

export function EntriesPage() {
  const queryClient = useQueryClient()
  const navigate = useNavigate()

  const entries = useQuery({ queryKey: entriesKey, queryFn: listEntries })

  const create = useMutation({
    mutationFn: createEntry,
    onSuccess: (entry) => {
      void queryClient.invalidateQueries({ queryKey: entriesKey })
      void navigate({ to: '/entries/$entryId', params: { entryId: entry.id } })
    },
  })

  const remove = useMutation({
    mutationFn: deleteEntry,
    onSettled: () =>
      queryClient.invalidateQueries({ queryKey: entriesKey }),
  })

  return (
    <main>
      <header className="page-header">
        <h1>Entries</h1>
        <button onClick={() => create.mutate()} disabled={create.isPending}>
          New entry
        </button>
      </header>

      {create.isError && <p role="alert">Could not create entry.</p>}
      {remove.isError && <p role="alert">Could not delete entry.</p>}

      {entries.isPending && <p>Loading…</p>}
      {entries.isError && <p role="alert">Could not load entries.</p>}
      {entries.isSuccess && entries.data.length === 0 && (
        <p>No entries yet. Create one to get started.</p>
      )}

      {entries.isSuccess && entries.data.length > 0 && (
        <ul className="entries">
          {entries.data.map((entry) => (
            <li key={entry.id}>
              <Link to="/entries/$entryId" params={{ entryId: entry.id }}>
                {entry.title}
              </Link>
              <time dateTime={new Date(entry.updatedAt).toISOString()}>
                {new Date(entry.updatedAt).toLocaleString()}
              </time>
              <button
                onClick={() => {
                  if (confirm(`Delete "${entry.title}"?`)) {
                    remove.mutate(entry.id)
                  }
                }}
                disabled={remove.isPending && remove.variables === entry.id}
              >
                Delete
              </button>
            </li>
          ))}
        </ul>
      )}
    </main>
  )
}
