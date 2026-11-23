import {
  Outlet,
  createRootRoute,
  createRoute,
  createRouter,
} from '@tanstack/react-router'
import { EntriesPage } from './EntriesPage.tsx'
import { EntryPage } from './EntryPage.tsx'

const rootRoute = createRootRoute({ component: () => <Outlet /> })

const indexRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/',
  // Why the user was sent here, if they were: shown on the list page.
  validateSearch: (search: Record<string, unknown>): { notice?: 'deleted' } =>
    search.notice === 'deleted' ? { notice: 'deleted' } : {},
  component: EntriesPage,
})

const entryRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/entries/$entryId',
  component: function EntryRoute() {
    const { entryId } = entryRoute.useParams()
    return <EntryPage entryId={entryId} />
  },
})

export const router = createRouter({
  routeTree: rootRoute.addChildren([indexRoute, entryRoute]),
})

declare module '@tanstack/react-router' {
  interface Register {
    router: typeof router
  }
}
