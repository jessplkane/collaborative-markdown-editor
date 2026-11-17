import {
  Outlet,
  createRootRoute,
  createRoute,
  createRouter,
} from '@tanstack/react-router'
import { EchoPage } from './EchoPage.tsx'

const rootRoute = createRootRoute({ component: () => <Outlet /> })

const indexRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/',
  component: EchoPage,
})

export const router = createRouter({
  routeTree: rootRoute.addChildren([indexRoute]),
})

declare module '@tanstack/react-router' {
  interface Register {
    router: typeof router
  }
}
