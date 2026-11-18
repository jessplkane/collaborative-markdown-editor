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
