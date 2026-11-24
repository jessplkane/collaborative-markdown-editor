# Deploying

The frontend and the server deploy separately. Environment variables are listed in [development.md](development.md#configuration).

## Frontend (Netlify)

`netlify.toml` at the repo root builds `web/` (`npm run build`) and publishes `web/dist`, with `VITE_API_URL` set to the server's URL at build time. It also rewrites unknown paths to `index.html`, which client-side routing needs. Publishing the unbuilt `web/` directory instead makes the browser request `/src/main.tsx` and fail with a MIME-type error for module scripts.

## Server (Fly.io)

`server/Dockerfile` and `server/fly.toml` build and run the Go server. It needs a long-lived process and a persistent disk, so it cannot run on serverless functions.

- **Database on the volume.** `fly.toml` mounts a volume named `data` at `/data` and sets `DB_PATH=/data/entries.db`. A Fly machine's root filesystem is wiped on every restart and deploy, so the database must live on the volume.
- **One machine only.** Rooms live in that process's memory, so two machines would split an entry's editors into separate rooms. Use `fly scale count 1`, and `fly deploy --ha=false` on the first deploy.
- **Allowed origins.** Set `ALLOWED_ORIGINS` (uncomment it in `fly.toml`, or use `fly secrets set`) to the frontend's host.
- **Shutdown.** Rooms save and close within Fly's five-second kill timeout; see [Shutdown](architecture.md#shutdown).
